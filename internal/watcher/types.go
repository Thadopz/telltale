package watcher

import (
	"time"

	"github.com/Thadopz/telltale/internal/diagnosis"
)

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

	DiagnosisJobPending   = "pending"
	DiagnosisJobRunning   = "running"
	DiagnosisJobSucceeded = "succeeded"
	DiagnosisJobFailed    = "failed"
)

type Config struct {
	Enabled             bool            `json:"enabled"`
	IntervalSeconds     int             `json:"interval_seconds"`
	QueryTimeoutSeconds int             `json:"query_timeout_seconds"`
	TenantID            string          `json:"tenant_id"`
	Environment         string          `json:"environment"`
	Diagnosis           DiagnosisConfig `json:"diagnosis"`
	Rules               []Rule          `json:"rules"`
}

type DiagnosisConfig struct {
	Enabled             bool  `json:"enabled"`
	PollIntervalSeconds int   `json:"poll_interval_seconds"`
	RunTimeoutSeconds   int   `json:"run_timeout_seconds"`
	RetryDelaysSeconds  []int `json:"retry_delays_seconds"`
}

type Rule struct {
	ID              string            `json:"id"`
	Domain          string            `json:"domain"`
	Collector       string            `json:"collector"`
	Service         string            `json:"service"`
	Metric          string            `json:"metric"`
	WindowMinutes   int               `json:"window_minutes"`
	ValuePath       string            `json:"value_path"`
	SampleCountPath string            `json:"sample_count_path"`
	Operator        string            `json:"operator"`
	Threshold       float64           `json:"threshold"`
	MinimumSamples  int               `json:"minimum_samples"`
	Severity        string            `json:"severity"`
	FiringWindows   int               `json:"firing_windows"`
	RecoveryWindows int               `json:"recovery_windows"`
	AutoDiagnose    bool              `json:"auto_diagnose"`
	MatchLabels     map[string]string `json:"match_labels"`
	AlertStates     []string          `json:"alert_states"`
	DatabaseProbe   DatabaseProbe     `json:"database_probe"`
}

type DatabaseProbe struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"`
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
	ID           string     `json:"id"`
	Fingerprint  string     `json:"fingerprint"`
	RuleID       string     `json:"rule_id"`
	Domain       string     `json:"domain"`
	Service      string     `json:"service"`
	Environment  string     `json:"environment"`
	Status       string     `json:"status"`
	Severity     string     `json:"severity"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	LatestValue  float64    `json:"latest_value"`
	EvidenceID   string     `json:"evidence_id,omitempty"`
	EvidenceJSON string     `json:"-"`
	Diagnosis    string     `json:"diagnosis,omitempty"`
}

type DiagnosisJob struct {
	ID              string                            `json:"id"`
	IncidentID      string                            `json:"incident_id"`
	EpisodeOpenedAt time.Time                         `json:"episode_opened_at"`
	Request         diagnosis.IncidentDiagnoseRequest `json:"request"`
	Status          string                            `json:"status"`
	AttemptCount    int                               `json:"attempt_count"`
	NextAttemptAt   time.Time                         `json:"next_attempt_at"`
	LeaseUntil      *time.Time                        `json:"lease_until,omitempty"`
	ConversationID  string                            `json:"conversation_id"`
	RunID           string                            `json:"run_id,omitempty"`
	ResultJSON      string                            `json:"result_json,omitempty"`
	LastError       string                            `json:"last_error,omitempty"`
	StartedAt       *time.Time                        `json:"started_at,omitempty"`
	CompletedAt     *time.Time                        `json:"completed_at,omitempty"`
	CreatedAt       time.Time                         `json:"created_at"`
	UpdatedAt       time.Time                         `json:"updated_at"`
}

type IncidentQuery struct {
	Status  string
	Service string
	Limit   int
}

type IncidentDetail struct {
	Incident Incident       `json:"incident"`
	Jobs     []DiagnosisJob `json:"diagnosis_jobs"`
}

type RuleResult struct {
	Rule       Rule
	Snapshot   Snapshot
	Abnormal   bool
	Transition string
	State      DetectorState
	Err        error
}
