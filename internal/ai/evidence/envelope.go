package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	StatusSuccess          = "success"
	StatusError            = "error"
	StatusTimeout          = "timeout"
	StatusUnknown          = "unknown"
	StatusPermissionDenied = "permission_denied"
	StatusProtocolError    = "protocol_error"

	DataPresent = "present"
	DataEmpty   = "empty"
	DataUnknown = "unknown"
	DataPartial = "partial"
)

type Envelope struct {
	Success          bool               `json:"success"`
	Status           string             `json:"status"`
	EvidenceID       string             `json:"evidence_id,omitempty"`
	FailedQueryID    string             `json:"failed_query_id,omitempty"`
	CanUseAsEvidence bool               `json:"can_use_as_evidence"`
	DataStatus       string             `json:"data_status"`
	Data             any                `json:"data,omitempty"`
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
	Subject          SubjectRef         `json:"subject,omitempty"`
	Scope            EvidenceScope      `json:"scope,omitempty"`
	Observations     []Observation      `json:"observations,omitempty"`
	Error            string             `json:"error,omitempty"`
	ErrorCode        string             `json:"error_code,omitempty"`
	FailureReason    string             `json:"failure_reason,omitempty"`
	Message          string             `json:"message,omitempty"`
	Summary          string             `json:"summary,omitempty"`
}

type Parsed struct {
	Envelope Envelope
	IsJSON   bool
}

func NewData(data any, message string) Envelope {
	return NewDataWithObservations(data, message, nil)
}

func NewDataWithObservations(data any, message string, observations []Observation) Envelope {
	dataStatus := DataPresent
	if isEmptyData(data) {
		dataStatus = DataEmpty
	}
	env := Envelope{
		Success:          true,
		Status:           StatusSuccess,
		EvidenceID:       newID("ev"),
		CanUseAsEvidence: true,
		DataStatus:       dataStatus,
		Data:             data,
		Observations:     normalizeObservations(observations),
		Message:          message,
		Summary:          message,
	}
	env.SourceKind, env.RuntimeVerified = inferSourceMetadata(env.Observations)
	env.Subject, env.Scope = firstSubjectScope(env.Observations)
	return env
}

func NewDataWithResultMetadata(data any, message string, observations []Observation, metadata ResultMetadata) Envelope {
	env := NewDataWithObservations(data, message, observations)
	env.ResultMetadata = metadata
	if metadata.Truncated {
		env.DataStatus = DataPartial
	}
	return env
}

func NewError(err error, message string) Envelope {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	status := StatusError
	code := "ERROR"
	lower := strings.ToLower(message + " " + errMsg)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") {
		status = StatusTimeout
		code = "TIMEOUT"
	}
	if strings.Contains(lower, "authorization denied") || strings.Contains(lower, "permission") {
		status = StatusPermissionDenied
		code = "PERMISSION_DENIED"
	}
	return Envelope{
		Success:          false,
		Status:           status,
		FailedQueryID:    newID("fq"),
		CanUseAsEvidence: false,
		DataStatus:       DataUnknown,
		Error:            errMsg,
		ErrorCode:        code,
		Message:          message,
		Summary:          message,
	}
}

func NewUnstructuredResult(toolName string) Envelope {
	return Envelope{
		Success:       false,
		Status:        StatusProtocolError,
		FailedQueryID: newID("fq"),
		DataStatus:    DataUnknown,
		ErrorCode:     "UNSTRUCTURED_TOOL_RESULT",
		FailureReason: "unstructured_tool_result",
		Message:       "tool " + strings.TrimSpace(toolName) + " returned an invalid evidence envelope",
		Summary:       "tool returned an unstructured result",
	}
}

// NewUnknownOperation is returned when a write may still be running after a
// client-side timeout. Callers must query the operation state before retrying.
func NewUnknownOperation(toolName string, operationID string, timeout time.Duration) Envelope {
	return Envelope{
		Success:     false,
		Status:      StatusUnknown,
		DataStatus:  DataUnknown,
		OperationID: strings.TrimSpace(operationID),
		ErrorCode:   "OPERATION_STATE_UNKNOWN",
		Message:     fmt.Sprintf("write tool %s may still be running after %s; query operation state before retrying", strings.TrimSpace(toolName), timeout),
		Summary:     "write operation state is unknown after timeout",
	}
}

func Marshal(env Envelope) string {
	normalize(&env)
	b, err := json.Marshal(env)
	if err != nil {
		fallback := NewError(err, "marshal tool output failed")
		b, _ = json.Marshal(fallback)
	}
	return string(b)
}

func Parse(raw string) Parsed {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Parsed{Envelope: NewError(nil, "empty tool response")}
	}
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return Parsed{
			IsJSON: false,
			Envelope: Envelope{
				Success:          true,
				Status:           StatusSuccess,
				CanUseAsEvidence: false,
				DataStatus:       DataUnknown,
				Message:          truncate(raw, 300),
				Summary:          truncate(raw, 300),
			},
		}
	}
	normalize(&env)
	return Parsed{Envelope: env, IsJSON: true}
}

func normalize(env *Envelope) {
	if env == nil {
		return
	}
	if strings.TrimSpace(env.Status) == "" {
		if env.Success {
			env.Status = StatusSuccess
		} else {
			env.Status = StatusError
		}
	}
	env.Status = strings.ToLower(strings.TrimSpace(env.Status))
	if env.Status == StatusSuccess {
		env.Success = true
		if strings.TrimSpace(env.DataStatus) == "" {
			if isEmptyData(env.Data) {
				env.DataStatus = DataEmpty
			} else {
				env.DataStatus = DataPresent
			}
		}
		if strings.TrimSpace(env.EvidenceID) == "" {
			env.CanUseAsEvidence = false
		}
		env.Observations = normalizeObservations(env.Observations)
		if env.SourceKind == "" {
			env.SourceKind, env.RuntimeVerified = inferSourceMetadata(env.Observations)
		}
		if strings.TrimSpace(env.Subject.Service) == "" && strings.TrimSpace(env.Subject.OrderNo) == "" && strings.TrimSpace(env.Subject.TraceID) == "" {
			env.Subject, env.Scope = firstSubjectScope(env.Observations)
		}
	} else {
		env.Success = false
		env.CanUseAsEvidence = false
		env.EvidenceID = ""
		env.Observations = nil
		if strings.TrimSpace(env.FailedQueryID) == "" {
			env.FailedQueryID = newID("fq")
		}
		if strings.TrimSpace(env.DataStatus) == "" {
			env.DataStatus = DataUnknown
		}
	}
	if strings.TrimSpace(env.Summary) == "" {
		env.Summary = env.Message
	}
	if strings.TrimSpace(env.Summary) == "" && env.Error != "" {
		env.Summary = env.Error
	}
}

func inferSourceMetadata(observations []Observation) (EvidenceSourceKind, bool) {
	if len(observations) == 0 {
		return "", false
	}
	var source EvidenceSourceKind
	for _, observation := range observations {
		candidate := sourceKindForObservation(observation.Kind)
		if candidate == "" {
			return "", false
		}
		if source == "" {
			source = candidate
			continue
		}
		if source != candidate {
			return "", false
		}
	}
	return source, source == SourceRuntimeMetric || source == SourceRuntimeLog || source == SourceRuntimeDB
}

func sourceKindForObservation(kind string) EvidenceSourceKind {
	switch strings.TrimSpace(kind) {
	case ObservationMetricAnomaly, ObservationMetricValue, ObservationStatusDistribution:
		return SourceRuntimeMetric
	case ObservationLogError:
		return SourceRuntimeLog
	case ObservationOrderState, ObservationStateMismatch, ObservationSlowSQL,
		ObservationDatabaseHealth, ObservationActiveQuery, ObservationWaitEvent, ObservationLock:
		return SourceRuntimeDB
	case ObservationStaticCodeDeclaration, ObservationStaticCodeCallSite, ObservationStaticRoute, ObservationStaticSQL:
		return SourceStaticCode
	case ObservationStaticConfig:
		return SourceStaticConfig
	default:
		return ""
	}
}

func normalizeObservations(observations []Observation) []Observation {
	out := make([]Observation, 0, len(observations))
	for _, obs := range observations {
		obs.Kind = strings.TrimSpace(obs.Kind)
		if !ValidObservation(obs) {
			continue
		}
		out = append(out, obs)
	}
	return out
}

func firstSubjectScope(observations []Observation) (SubjectRef, EvidenceScope) {
	for _, obs := range observations {
		return obs.Subject, obs.Scope
	}
	return SubjectRef{}, EvidenceScope{}
}

func isEmptyData(data any) bool {
	if data == nil {
		return true
	}
	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return v.Len() == 0
	default:
		return false
	}
}

func newID(prefix string) string {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(bytes[:]))
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
