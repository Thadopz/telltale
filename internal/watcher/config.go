package watcher

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Thadopz/telltale/internal/ai/sqlobserve"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	defaultInterval              = time.Minute
	defaultQueryTimeout          = 15 * time.Second
	defaultDiagnosisPollInterval = 2 * time.Second
	defaultDiagnosisRunTimeout   = 5 * time.Minute
	maxMatchLabels               = 8
	maxLabelValueRunes           = 128
)

var prometheusLabelNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var defaultDiagnosisRetryDelays = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

func LoadConfig(ctx context.Context) (Config, error) {
	value, err := g.Cfg().Get(ctx, "watch")
	if err != nil || value == nil || value.IsEmpty() {
		return Config{}, nil
	}

	var config Config
	if err = value.Scan(&config); err != nil {
		return Config{}, fmt.Errorf("parse watch config: %w", err)
	}
	applyConfigDefaults(&config)
	if config.Enabled {
		if err = validateWatchScope(config); err != nil {
			return Config{}, err
		}
		if err = validateDiagnosisConfig(config.Diagnosis); err != nil {
			return Config{}, err
		}
		if err = validateRules(config.Rules); err != nil {
			return Config{}, err
		}
		if err = validateDatabaseProbes(ctx, config.Rules); err != nil {
			return Config{}, err
		}
	}
	return config, nil
}

func validateWatchScope(config Config) error {
	if !validWatchMetadata(config.TenantID) {
		return fmt.Errorf("watch tenant_id contains invalid characters")
	}
	if !validWatchMetadata(config.Environment) {
		return fmt.Errorf("watch environment contains invalid characters")
	}
	return nil
}

func validateDiagnosisConfig(config DiagnosisConfig) error {
	if !config.Enabled {
		return nil
	}
	if config.PollIntervalSeconds <= 0 {
		return fmt.Errorf("watch diagnosis poll_interval_seconds must be positive")
	}
	if config.RunTimeoutSeconds <= 0 {
		return fmt.Errorf("watch diagnosis run_timeout_seconds must be positive")
	}
	if len(config.RetryDelaysSeconds) != 3 {
		return fmt.Errorf("watch diagnosis retry_delays_seconds must contain exactly three delays")
	}
	for _, delay := range config.RetryDelaysSeconds {
		if delay <= 0 {
			return fmt.Errorf("watch diagnosis retry delays must be positive")
		}
	}
	return nil
}

func (c Config) Interval() time.Duration {
	if c.IntervalSeconds <= 0 {
		return defaultInterval
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}

func (c Config) QueryTimeout() time.Duration {
	if c.QueryTimeoutSeconds <= 0 {
		return defaultQueryTimeout
	}
	return time.Duration(c.QueryTimeoutSeconds) * time.Second
}

func (c Config) DiagnosisPollInterval() time.Duration {
	if c.Diagnosis.PollIntervalSeconds <= 0 {
		return defaultDiagnosisPollInterval
	}
	return time.Duration(c.Diagnosis.PollIntervalSeconds) * time.Second
}

func (c Config) DiagnosisRunTimeout() time.Duration {
	if c.Diagnosis.RunTimeoutSeconds <= 0 {
		return defaultDiagnosisRunTimeout
	}
	return time.Duration(c.Diagnosis.RunTimeoutSeconds) * time.Second
}

func (c Config) DiagnosisRetryDelays() []time.Duration {
	if len(c.Diagnosis.RetryDelaysSeconds) == 0 {
		return append([]time.Duration(nil), defaultDiagnosisRetryDelays...)
	}
	delays := make([]time.Duration, 0, len(c.Diagnosis.RetryDelaysSeconds))
	for _, seconds := range c.Diagnosis.RetryDelaysSeconds {
		delays = append(delays, time.Duration(seconds)*time.Second)
	}
	return delays
}

func applyConfigDefaults(config *Config) {
	if config == nil {
		return
	}
	if config.IntervalSeconds <= 0 {
		config.IntervalSeconds = int(defaultInterval / time.Second)
	}
	if config.QueryTimeoutSeconds <= 0 {
		config.QueryTimeoutSeconds = int(defaultQueryTimeout / time.Second)
	}
	if config.Diagnosis.PollIntervalSeconds <= 0 {
		config.Diagnosis.PollIntervalSeconds = int(defaultDiagnosisPollInterval / time.Second)
	}
	if config.Diagnosis.RunTimeoutSeconds <= 0 {
		config.Diagnosis.RunTimeoutSeconds = int(defaultDiagnosisRunTimeout / time.Second)
	}
	if len(config.Diagnosis.RetryDelaysSeconds) == 0 {
		config.Diagnosis.RetryDelaysSeconds = []int{30, 120, 600}
	}
	config.TenantID = strings.TrimSpace(config.TenantID)
	if config.TenantID == "" {
		config.TenantID = "default"
	}
	config.Environment = strings.TrimSpace(config.Environment)
	if config.Environment == "" {
		config.Environment = "default"
	}
	for index := range config.Rules {
		rule := &config.Rules[index]
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Domain = strings.ToLower(strings.TrimSpace(rule.Domain))
		rule.Collector = strings.ToLower(strings.TrimSpace(rule.Collector))
		rule.Service = strings.TrimSpace(rule.Service)
		rule.DatabaseProbe.Target = strings.TrimSpace(rule.DatabaseProbe.Target)
		rule.Metric = strings.TrimSpace(rule.Metric)
		rule.ValuePath = strings.TrimSpace(rule.ValuePath)
		rule.SampleCountPath = strings.TrimSpace(rule.SampleCountPath)
		rule.Operator = strings.ToLower(strings.TrimSpace(rule.Operator))
		rule.Severity = strings.ToLower(strings.TrimSpace(rule.Severity))
		for key, value := range rule.MatchLabels {
			trimmedKey := strings.TrimSpace(key)
			trimmedValue := strings.TrimSpace(value)
			if trimmedKey != key {
				delete(rule.MatchLabels, key)
			}
			rule.MatchLabels[trimmedKey] = trimmedValue
		}
		for stateIndex := range rule.AlertStates {
			rule.AlertStates[stateIndex] = strings.ToLower(strings.TrimSpace(rule.AlertStates[stateIndex]))
		}
		if rule.WindowMinutes <= 0 {
			rule.WindowMinutes = 5
		}
		if rule.FiringWindows <= 0 {
			rule.FiringWindows = 3
		}
		if rule.RecoveryWindows <= 0 {
			rule.RecoveryWindows = 2
		}
		if rule.Severity == "" {
			rule.Severity = "warning"
		}
	}
}

func validateDatabaseProbes(ctx context.Context, rules []Rule) error {
	for _, rule := range rules {
		if !rule.DatabaseProbe.Enabled {
			continue
		}
		if rule.Domain != "observability" {
			return fmt.Errorf("watch rule %s database_probe requires observability domain", rule.ID)
		}
		if rule.DatabaseProbe.Target == "" {
			return fmt.Errorf("watch rule %s database_probe target is required", rule.ID)
		}
		if _, err := sqlobserve.ConfiguredTargetEngine(ctx, rule.DatabaseProbe.Target); err != nil {
			return fmt.Errorf("watch rule %s database_probe: %w", rule.ID, err)
		}
	}
	return nil
}

func validateRules(rules []Rule) error {
	if len(rules) == 0 {
		return fmt.Errorf("watch.enabled=true requires at least one rule")
	}
	seenIDs := make(map[string]bool)
	for _, rule := range rules {
		if rule.ID == "" {
			return fmt.Errorf("watch rule has no id")
		}
		if !validWatchMetadata(rule.ID) {
			return fmt.Errorf("watch rule id %q contains invalid characters", rule.ID)
		}
		if !validWatchMetadata(rule.Service) {
			return fmt.Errorf("watch rule %s service contains invalid characters", rule.ID)
		}
		if !validWatchMetadata(rule.Severity) {
			return fmt.Errorf("watch rule %s severity contains invalid characters", rule.ID)
		}
		if seenIDs[rule.ID] {
			return fmt.Errorf("watch rules contain duplicate id %q", rule.ID)
		}
		seenIDs[rule.ID] = true
		switch rule.Domain {
		case "payment", "observability":
		default:
			return fmt.Errorf("watch rule %s has unsupported domain %q", rule.ID, rule.Domain)
		}
		switch rule.Collector {
		case CollectorPaymentMetric, CollectorPaymentStatusDistribution, CollectorPrometheusAlerts:
		default:
			return fmt.Errorf("watch rule %s has unsupported collector %q", rule.ID, rule.Collector)
		}
		if strings.HasPrefix(rule.Collector, "payment_") && rule.Domain != "payment" {
			return fmt.Errorf("watch rule %s payment collector requires domain payment", rule.ID)
		}
		if rule.Collector == CollectorPrometheusAlerts && rule.Domain != "observability" {
			return fmt.Errorf("watch rule %s prometheus collector requires domain observability", rule.ID)
		}
		switch rule.Operator {
		case OperatorLessThan, OperatorGreaterThan, OperatorCountAbove:
		default:
			return fmt.Errorf("watch rule %s has unsupported operator %q", rule.ID, rule.Operator)
		}
		if rule.Operator != OperatorCountAbove && rule.ValuePath == "" {
			return fmt.Errorf("watch rule %s requires value_path", rule.ID)
		}
		if rule.MinimumSamples > 0 && rule.SampleCountPath == "" {
			return fmt.Errorf("watch rule %s requires sample_count_path when minimum_samples is set", rule.ID)
		}
		if err := validatePrometheusFilters(rule); err != nil {
			return err
		}
	}
	return nil
}

func validatePrometheusFilters(rule Rule) error {
	if len(rule.MatchLabels) == 0 && len(rule.AlertStates) == 0 {
		return nil
	}
	if rule.Collector != CollectorPrometheusAlerts {
		return fmt.Errorf("watch rule %s alert filters require prometheus_alerts collector", rule.ID)
	}
	if len(rule.MatchLabels) > maxMatchLabels {
		return fmt.Errorf("watch rule %s match_labels exceeds %d entries", rule.ID, maxMatchLabels)
	}
	for key, value := range rule.MatchLabels {
		if !prometheusLabelNamePattern.MatchString(key) {
			return fmt.Errorf("watch rule %s has invalid Prometheus label name %q", rule.ID, key)
		}
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxLabelValueRunes || containsControlCharacter(value) {
			return fmt.Errorf("watch rule %s has invalid value for Prometheus label %q", rule.ID, key)
		}
	}
	for _, state := range rule.AlertStates {
		if state != "firing" && state != "pending" {
			return fmt.Errorf("watch rule %s has unsupported Prometheus alert state %q", rule.ID, state)
		}
	}
	return nil
}

func containsControlCharacter(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func validWatchMetadata(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("-_.:/@|", character) {
			continue
		}
		return false
	}
	return true
}
