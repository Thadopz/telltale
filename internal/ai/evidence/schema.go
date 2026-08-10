package evidence

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

type EvidenceSourceKind string

const (
	SourceRuntimeMetric EvidenceSourceKind = "runtime_metric"
	SourceRuntimeLog    EvidenceSourceKind = "runtime_log"
	SourceRuntimeDB     EvidenceSourceKind = "runtime_database"
	SourceStaticCode    EvidenceSourceKind = "static_code"
	SourceStaticConfig  EvidenceSourceKind = "static_config"
)

type VersionAlignment string

const (
	VersionAligned      VersionAlignment = "aligned"
	VersionMismatch     VersionAlignment = "mismatch"
	VersionUnknown      VersionAlignment = "unknown"
	VersionUnverifiable VersionAlignment = "unverifiable"
)

const ContentTrustUntrustedRepository = "untrusted_repository_content"

const (
	ContentTrustTrustedLocal      = "trusted_local_tool"
	ContentTrustUntrustedExternal = "untrusted_external_result"
	ContentTrustUnknown           = "unknown"
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
	ClaimImplementationContains    = "implementation_contains"
	ClaimImplementationMayCall     = "implementation_may_call"
	ClaimConfigValueDefined        = "config_value_defined"
	ClaimErrorMappedAtCodeLocation = "error_mapped_at_code_location"

	ObservationMetricAnomaly         = "metric_anomaly"
	ObservationMetricValue           = "metric_value"
	ObservationStatusDistribution    = "status_distribution"
	ObservationOrderState            = "order_state"
	ObservationStateMismatch         = "state_mismatch"
	ObservationLogError              = "log_error"
	ObservationSlowSQL               = "slow_sql"
	ObservationDatabaseHealth        = "database_health"
	ObservationActiveQuery           = "active_query"
	ObservationWaitEvent             = "wait_event"
	ObservationLock                  = "lock"
	ObservationRunbookRule           = "runbook_rule"
	ObservationEmptyResult           = "empty_result"
	ObservationMCPToolResult         = "mcp_tool_result"
	ObservationStaticCodeDeclaration = "static_code_declaration"
	ObservationStaticCodeCallSite    = "static_code_call_site"
	ObservationStaticConfig          = "static_config"
	ObservationStaticRoute           = "static_route"
	ObservationStaticSQL             = "static_sql"

	CoverageCovered             = "covered"
	CoveragePartial             = "partial"
	CoverageUncovered           = "uncovered"
	CoverageUnknown             = "unknown"
	CoverageConflict            = "conflict"
	CoverageSupported           = "supported"
	CoverageRefuted             = "refuted"
	CoverageStale               = "stale"
	CoverageActionOnly          = "action_only"
	CoverageStaticallySupported = "statically_supported"
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

// ResultMetadata explains how a query result was reduced before it reached
// the agent. A partial result only describes the returned scope.
type ResultMetadata struct {
	ReturnedRows     int      `json:"returned_rows,omitempty"`
	MaxRows          int      `json:"max_rows,omitempty"`
	ReturnedBytes    int      `json:"returned_bytes,omitempty"`
	MaxBytes         int      `json:"max_bytes,omitempty"`
	Truncated        bool     `json:"truncated,omitempty"`
	TruncationReason string   `json:"truncation_reason,omitempty"`
	Projection       []string `json:"projection,omitempty"`
	SortBy           string   `json:"sort_by,omitempty"`
	Aggregation      string   `json:"aggregation,omitempty"`
}

type Observation struct {
	Kind    string         `json:"kind"`
	Subject SubjectRef     `json:"subject,omitempty"`
	Scope   EvidenceScope  `json:"scope,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

type EvidenceConflict struct {
	EvidenceID     string `json:"evidence_id"`
	FirstDigest    string `json:"first_digest,omitempty"`
	SecondDigest   string `json:"second_digest,omitempty"`
	FirstToolName  string `json:"first_tool_name,omitempty"`
	SecondToolName string `json:"second_tool_name,omitempty"`
}

type EvidenceProtocolIssue struct {
	ToolName string `json:"tool_name"`
	Reason   string `json:"reason"`
}

type EvidenceItem struct {
	EvidenceID       string             `json:"evidence_id"`
	ToolName         string             `json:"tool_name"`
	SourceKind       EvidenceSourceKind `json:"source_kind,omitempty"`
	Repository       string             `json:"repository,omitempty"`
	Revision         string             `json:"revision,omitempty"`
	SnapshotDigest   string             `json:"snapshot_digest,omitempty"`
	VersionAlignment VersionAlignment   `json:"version_alignment,omitempty"`
	RuntimeVerified  bool               `json:"runtime_verified"`
	ContentTrust     string             `json:"content_trust,omitempty"`
	Issuer           string             `json:"issuer,omitempty"`
	RunID            string             `json:"run_id,omitempty"`
	ConversationID   string             `json:"conversation_id,omitempty"`
	TenantID         string             `json:"tenant_id,omitempty"`
	ObservedAt       time.Time          `json:"observed_at,omitempty"`
	ExpiresAt        *time.Time         `json:"expires_at,omitempty"`
	ContentDigest    string             `json:"content_digest,omitempty"`
	TrustLevel       string             `json:"trust_level,omitempty"`
	OperationID      string             `json:"operation_id,omitempty"`
	ResultMetadata   ResultMetadata     `json:"result_metadata,omitempty"`
	Scope            EvidenceScope      `json:"scope,omitempty"`
	Subject          SubjectRef         `json:"subject,omitempty"`
	Observations     []Observation      `json:"observations"`
	RawOutput        json.RawMessage    `json:"raw_output,omitempty"`
	Summary          string             `json:"summary,omitempty"`
}

type FailedQuery struct {
	FailedQueryID string `json:"failed_query_id"`
	ToolName      string `json:"tool_name"`
	Status        string `json:"status"`
	Summary       string `json:"summary,omitempty"`
}

type CandidateClaim struct {
	ClaimID        string         `json:"claim_id"`
	ClaimType      string         `json:"claim_type"`
	AssertionKind  string         `json:"assertion_kind"`
	Subject        SubjectRef     `json:"subject,omitempty"`
	Scope          EvidenceScope  `json:"scope,omitempty"`
	Predicate      string         `json:"predicate,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
	EvidenceIDs    []string       `json:"evidence_ids,omitempty"`
	FailedQueryIDs []string       `json:"failed_query_ids,omitempty"`
}

func ValidClaimType(claimType string) bool {
	_, ok := ClaimRulesWithPayment()[strings.TrimSpace(claimType)]
	return ok
}

func ValidObservation(obs Observation) bool {
	switch strings.TrimSpace(obs.Kind) {
	case ObservationMetricAnomaly,
		ObservationMetricValue,
		ObservationStatusDistribution,
		ObservationOrderState,
		ObservationStateMismatch,
		ObservationLogError,
		ObservationSlowSQL,
		ObservationDatabaseHealth,
		ObservationActiveQuery,
		ObservationWaitEvent,
		ObservationLock,
		ObservationRunbookRule,
		ObservationEmptyResult,
		ObservationMCPToolResult,
		ObservationStaticCodeDeclaration,
		ObservationStaticCodeCallSite,
		ObservationStaticConfig,
		ObservationStaticRoute,
		ObservationStaticSQL:
		return true
	default:
		return false
	}
}

type CodeAnchor struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	Value            string  `json:"value"`
	SourceEvidenceID string  `json:"source_evidence_id"`
	Confidence       float64 `json:"confidence"`
}

type AnchorStore struct {
	mu      sync.RWMutex
	anchors map[string]CodeAnchor
}

type anchorStoreContextKey struct{}

func NewAnchorStore() *AnchorStore {
	return &AnchorStore{anchors: make(map[string]CodeAnchor)}
}

func WithAnchorStore(ctx context.Context, store *AnchorStore) context.Context {
	return context.WithValue(ctx, anchorStoreContextKey{}, store)
}

func AnchorStoreFromContext(ctx context.Context) *AnchorStore {
	store, _ := ctx.Value(anchorStoreContextKey{}).(*AnchorStore)
	return store
}

func (store *AnchorStore) Add(anchor CodeAnchor) {
	if store == nil || strings.TrimSpace(anchor.ID) == "" || strings.TrimSpace(anchor.Value) == "" {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.anchors[anchor.ID] = anchor
}

func (store *AnchorStore) Get(id string) (CodeAnchor, bool) {
	if store == nil {
		return CodeAnchor{}, false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	anchor, ok := store.anchors[strings.TrimSpace(id)]
	return anchor, ok
}
