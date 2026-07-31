package watcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	defaultInterval     = time.Minute
	defaultQueryTimeout = 15 * time.Second
)

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
		if err = validateRules(config.Rules); err != nil {
			return Config{}, err
		}
	}
	return config, nil
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
		rule.Metric = strings.TrimSpace(rule.Metric)
		rule.ValuePath = strings.TrimSpace(rule.ValuePath)
		rule.SampleCountPath = strings.TrimSpace(rule.SampleCountPath)
		rule.Operator = strings.ToLower(strings.TrimSpace(rule.Operator))
		rule.Severity = strings.ToLower(strings.TrimSpace(rule.Severity))
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

func validateRules(rules []Rule) error {
	if len(rules) == 0 {
		return fmt.Errorf("watch.enabled=true requires at least one rule")
	}
	seenIDs := make(map[string]bool)
	for _, rule := range rules {
		if rule.ID == "" {
			return fmt.Errorf("watch rule has no id")
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
	}
	return nil
}
