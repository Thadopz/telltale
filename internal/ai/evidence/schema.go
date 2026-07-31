package evidence

import (
	"encoding/json"
	"strings"
)

const (
	ClaimMetricAnomaly      = "metric_anomaly"
	ClaimMetricValue        = "metric_value"
	ClaimLogError           = "log_error"
	ClaimStatusDistribution = "status_distribution"
	ClaimStateMismatch      = "state_mismatch"
	ClaimSlowSQL            = "slow_sql"
	ClaimRunbookRule        = "runbook_rule"

	ClaimPaymentMetricAbnormal     = "payment_metric_abnormal"
	ClaimPaymentOrderState         = "payment_order_state"
	ClaimPaymentStateMismatch      = "payment_state_mismatch"
	ClaimPaymentLogError           = "payment_log_error"
	ClaimPaymentStatusDistribution = "payment_status_distribution"
	ClaimCallbackTimeoutRelated    = "callback_timeout_related"
	ClaimCallbackTimeoutRootCause  = "callback_timeout_root_cause"

	ObservationMetricAnomaly      = "metric_anomaly"
	ObservationMetricValue        = "metric_value"
	ObservationStatusDistribution = "status_distribution"
	ObservationOrderState         = "order_state"
	ObservationStateMismatch      = "state_mismatch"
	ObservationLogError           = "log_error"
	ObservationSlowSQL            = "slow_sql"
	ObservationRunbookRule        = "runbook_rule"
	ObservationEmptyResult        = "empty_result"
	ObservationMCPToolResult      = "mcp_tool_result"

	CoverageCovered   = "covered"
	CoveragePartial   = "partial"
	CoverageUncovered = "uncovered"
	CoverageUnknown   = "unknown"
	CoverageConflict  = "conflict"
)

type SubjectRef struct {
	Type    string            `json:"type,omitempty"`
	Service string            `json:"service,omitempty"`
	OrderNo string            `json:"order_no,omitempty"`
	TraceID string            `json:"trace_id,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type EvidenceScope struct {
	TimeRange string            `json:"time_range,omitempty"`
	StartTime string            `json:"start_time,omitempty"`
	EndTime   string            `json:"end_time,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

type Observation struct {
	Kind    string         `json:"kind"`
	Subject SubjectRef     `json:"subject,omitempty"`
	Scope   EvidenceScope  `json:"scope,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

type EvidenceItem struct {
	EvidenceID   string          `json:"evidence_id"`
	ToolName     string          `json:"tool_name"`
	Scope        EvidenceScope   `json:"scope,omitempty"`
	Subject      SubjectRef      `json:"subject,omitempty"`
	Observations []Observation   `json:"observations"`
	RawOutput    json.RawMessage `json:"raw_output,omitempty"`
	Summary      string          `json:"summary,omitempty"`
}

type FailedQuery struct {
	FailedQueryID string `json:"failed_query_id"`
	ToolName      string `json:"tool_name"`
	Status        string `json:"status"`
	Summary       string `json:"summary,omitempty"`
}

type CandidateClaim struct {
	ClaimType string         `json:"claim_type"`
	Subject   SubjectRef     `json:"subject,omitempty"`
	Scope     EvidenceScope  `json:"scope,omitempty"`
	Predicate string         `json:"predicate,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func ValidClaimType(claimType string) bool {
	_, ok := ClaimRulesWithPayment()[strings.TrimSpace(claimType)]
	return ok
}

func ValidObservation(obs Observation) bool {
	return strings.TrimSpace(obs.Kind) != ""
}
