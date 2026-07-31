package watcher

import "time"

const (
	CollectorPaymentMetric             = "payment_metric"
	CollectorPaymentStatusDistribution = "payment_status_distribution"
	CollectorPrometheusAlerts          = "prometheus_alerts"

	OperatorLessThan    = "lt"
	OperatorGreaterThan = "gt"
	OperatorCountAbove  = "count_gt"

	StateNormal = "normal"
	StateFiring = "firing"

	TransitionNone     = ""
	TransitionOpened   = "opened"
	TransitionResolved = "resolved"
)

type Config struct {
	Enabled             bool   `json:"enabled"`
	IntervalSeconds     int    `json:"interval_seconds"`
	QueryTimeoutSeconds int    `json:"query_timeout_seconds"`
	TenantID            string `json:"tenant_id"`
	Environment         string `json:"environment"`
	Rules               []Rule `json:"rules"`
}

type Rule struct {
	ID              string  `json:"id"`
	Domain          string  `json:"domain"`
	Collector       string  `json:"collector"`
	Service         string  `json:"service"`
	Metric          string  `json:"metric"`
	WindowMinutes   int     `json:"window_minutes"`
	ValuePath       string  `json:"value_path"`
	SampleCountPath string  `json:"sample_count_path"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	MinimumSamples  int     `json:"minimum_samples"`
	Severity        string  `json:"severity"`
	FiringWindows   int     `json:"firing_windows"`
	RecoveryWindows int     `json:"recovery_windows"`
}

type Snapshot struct {
	RuleID      string
	CollectedAt time.Time
	Value       float64
	SampleCount int
	EvidenceID  string
	RawOutput   string
}

type DetectorState struct {
	StateKey             string
	RuleID               string
	State                string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastValue            float64
	LastEvidenceID       string
	LastError            string
	UpdatedAt            time.Time
}

type Incident struct {
	ID           string
	Fingerprint  string
	RuleID       string
	Domain       string
	Status       string
	Severity     string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	ResolvedAt   *time.Time
	LatestValue  float64
	EvidenceID   string
	EvidenceJSON string
}

type RuleResult struct {
	Rule       Rule
	Snapshot   Snapshot
	Abnormal   bool
	Transition string
	State      DetectorState
	Err        error
}
