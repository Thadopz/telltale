package evidence

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

const (
	ProtocolVersion = "2"

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

// Envelope is only the wire-level result union. Evidence, failure, and an
// indeterminate write operation are mutually exclusive payloads.
type Envelope struct {
	ProtocolVersion string          `json:"protocol_version"`
	Status          string          `json:"status"`
	DataStatus      string          `json:"data_status"`
	Data            any             `json:"data,omitempty"`
	Evidence        *EvidenceRecord `json:"evidence,omitempty"`
	Failure         *FailureRecord  `json:"failure,omitempty"`
	Operation       *OperationState `json:"operation,omitempty"`
	Summary         string          `json:"summary,omitempty"`
}

type FailureRecord struct {
	QueryID string `json:"query_id"`
	Code    string `json:"code"`
	Reason  string `json:"reason,omitempty"`
}

type OperationState struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
}

type Parsed struct {
	Envelope Envelope
	IsJSON   bool
}

func NewData(data any, summary string) Envelope {
	return NewDataWithObservations(data, summary, nil)
}

func NewDataWithObservations(data any, summary string, observations []Observation) Envelope {
	normalized := normalizeObservations(observations)
	env := Envelope{
		ProtocolVersion: ProtocolVersion,
		Status:          StatusSuccess,
		DataStatus:      dataStatusFor(data),
		Data:            data,
		Summary:         strings.TrimSpace(summary),
	}
	if len(normalized) == 0 {
		return env
	}
	sourceKind, runtimeVerified := inferSourceMetadata(normalized)
	env.Evidence = &EvidenceRecord{
		EvidenceID: newID("ev"),
		Provenance: Provenance{
			SourceKind:      sourceKind,
			RuntimeVerified: runtimeVerified,
		},
		Observations: normalized,
	}
	return env
}

func NewDataWithResultMetadata(data any, summary string, observations []Observation, metadata ResultMetadata) Envelope {
	env := NewDataWithObservations(data, summary, observations)
	if env.Evidence != nil {
		env.Evidence.ResultMetadata = metadata
	}
	if metadata.Truncated {
		env.DataStatus = DataPartial
	}
	return env
}

func NewError(err error, summary string) Envelope {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	status := StatusError
	code := "ERROR"
	lower := strings.ToLower(summary + " " + detail)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") {
		status = StatusTimeout
		code = "TIMEOUT"
	}
	if strings.Contains(lower, "authorization denied") || strings.Contains(lower, "permission") {
		status = StatusPermissionDenied
		code = "PERMISSION_DENIED"
	}
	return newFailure(status, code, detail, summary)
}

func NewUnstructuredResult(toolName string) Envelope {
	toolName = strings.TrimSpace(toolName)
	summary := "tool returned a result that does not conform to evidence protocol v" + ProtocolVersion
	if toolName != "" {
		summary = "tool " + toolName + " returned a result that does not conform to evidence protocol v" + ProtocolVersion
	}
	return newFailure(StatusProtocolError, "INVALID_TOOL_RESULT", "protocol_version_mismatch", summary)
}

func NewProtocolError(code, reason, summary string) Envelope {
	return newFailure(StatusProtocolError, code, reason, summary)
}

func NewFailure(status, code, reason, summary string) Envelope {
	switch status {
	case StatusError, StatusTimeout, StatusPermissionDenied, StatusProtocolError:
		return newFailure(status, code, reason, summary)
	default:
		return NewProtocolError("INVALID_FAILURE_STATUS", status, "invalid failure status")
	}
}

func newFailure(status, code, reason, summary string) Envelope {
	return Envelope{
		ProtocolVersion: ProtocolVersion,
		Status:          status,
		DataStatus:      DataUnknown,
		Failure: &FailureRecord{
			QueryID: newID("fq"),
			Code:    strings.TrimSpace(code),
			Reason:  strings.TrimSpace(reason),
		},
		Summary: strings.TrimSpace(summary),
	}
}

// NewUnknownOperation is returned when a write may still be running after a
// client-side timeout. It is not a failed query and cannot support a claim.
func NewUnknownOperation(toolName string, operationID string, timeout time.Duration) Envelope {
	return Envelope{
		ProtocolVersion: ProtocolVersion,
		Status:          StatusUnknown,
		DataStatus:      DataUnknown,
		Operation: &OperationState{
			OperationID: strings.TrimSpace(operationID),
			State:       StatusUnknown,
		},
		Summary: fmt.Sprintf("write tool %s may still be running after %s; query operation state before retrying", strings.TrimSpace(toolName), timeout),
	}
}

func Marshal(env Envelope) string {
	if err := env.Validate(); err != nil {
		env = NewProtocolError("INVALID_RESULT_UNION", err.Error(), "tool produced an invalid evidence protocol result")
	}
	b, err := json.Marshal(env)
	if err == nil {
		return string(b)
	}
	fallback, _ := json.Marshal(NewProtocolError("MARSHAL_FAILED", err.Error(), "marshal tool output failed"))
	return string(fallback)
}

func Parse(raw string) Parsed {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Parsed{Envelope: NewProtocolError("EMPTY_TOOL_RESULT", "empty_tool_result", "empty tool response")}
	}
	var env Envelope
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		if !json.Valid([]byte(raw)) {
			return protocolGap(raw, "INVALID_TOOL_RESULT", "unstructured_tool_result", "tool result is not valid JSON", false)
		}
		return protocolGap(raw, "INVALID_RESULT_UNION", err.Error(), "tool result rejected by evidence protocol v"+ProtocolVersion, true)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return protocolGap(raw, "INVALID_RESULT_UNION", "multiple_json_values", "tool result rejected by evidence protocol v"+ProtocolVersion, true)
	}
	if err := env.Validate(); err != nil {
		return protocolGap(raw, "INVALID_RESULT_UNION", err.Error(), "tool result rejected by evidence protocol v"+ProtocolVersion, true)
	}
	return Parsed{Envelope: env, IsJSON: true}
}

func protocolGap(raw, code, reason, summary string, isJSON bool) Parsed {
	env := NewProtocolError(code, reason, summary)
	digest := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	env.Failure.QueryID = "fq_protocol_" + hex.EncodeToString(digest[:8])
	return Parsed{Envelope: env, IsJSON: isJSON}
}

func (env Envelope) Validate() error {
	if env.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol_version %q", env.ProtocolVersion)
	}
	if !validDataStatus(env.DataStatus) {
		return fmt.Errorf("invalid data_status %q", env.DataStatus)
	}
	switch env.Status {
	case StatusSuccess:
		if env.Failure != nil || env.Operation != nil {
			return fmt.Errorf("success result cannot contain failure or operation payload")
		}
		if env.Evidence != nil {
			if strings.TrimSpace(env.Evidence.EvidenceID) == "" {
				return fmt.Errorf("evidence payload requires evidence_id")
			}
			if len(env.Evidence.Observations) == 0 {
				return fmt.Errorf("evidence payload requires structured observations")
			}
			for _, observation := range env.Evidence.Observations {
				if !ValidObservation(observation) {
					return fmt.Errorf("evidence payload contains invalid observation kind %q", observation.Kind)
				}
			}
		}
	case StatusUnknown:
		if env.Operation == nil || env.Evidence != nil || env.Failure != nil {
			return fmt.Errorf("unknown result requires only an operation payload")
		}
		if strings.TrimSpace(env.Operation.OperationID) == "" {
			return fmt.Errorf("operation payload requires operation_id")
		}
	case StatusError, StatusTimeout, StatusPermissionDenied, StatusProtocolError:
		if env.Failure == nil || env.Evidence != nil || env.Operation != nil {
			return fmt.Errorf("failure result requires only a failure payload")
		}
		if strings.TrimSpace(env.Failure.QueryID) == "" || strings.TrimSpace(env.Failure.Code) == "" {
			return fmt.Errorf("failure payload requires query_id and code")
		}
	default:
		return fmt.Errorf("invalid status %q", env.Status)
	}
	return nil
}

func (env Envelope) UsableEvidence() (*EvidenceRecord, bool) {
	if env.Status != StatusSuccess || env.Evidence == nil || len(env.Evidence.Observations) == 0 {
		return nil, false
	}
	if strings.TrimSpace(env.Evidence.EvidenceID) == "" {
		return nil, false
	}
	return env.Evidence, true
}

func (env Envelope) ToEvidenceItem(toolName string) (EvidenceItem, bool) {
	record, ok := env.UsableEvidence()
	if !ok {
		return EvidenceItem{}, false
	}
	return EvidenceItem{
		EvidenceID:       record.EvidenceID,
		ToolName:         strings.TrimSpace(toolName),
		SourceKind:       record.Provenance.SourceKind,
		Repository:       record.Provenance.Repository,
		Revision:         record.Provenance.Revision,
		SnapshotDigest:   record.Provenance.SnapshotDigest,
		VersionAlignment: record.Provenance.VersionAlignment,
		RuntimeVerified:  record.Provenance.RuntimeVerified,
		PayloadTrust:     record.Provenance.PayloadTrust,
		Issuer:           record.Attestation.Issuer,
		RunID:            record.Attestation.RunID,
		ConversationID:   record.Attestation.ConversationID,
		TenantID:         record.Attestation.TenantID,
		ObservedAt:       record.Attestation.ObservedAt,
		ExpiresAt:        record.Attestation.ExpiresAt,
		ContentDigest:    record.Attestation.ContentDigest,
		DigestVersion:    record.Attestation.DigestVersion,
		IssuerTrust:      record.Attestation.IssuerTrust,
		ResultMetadata:   record.ResultMetadata,
		Observations:     append([]Observation(nil), record.Observations...),
		Summary:          env.Summary,
	}, true
}

func (env Envelope) ToFailedQuery(toolName string) (FailedQuery, bool) {
	if env.Failure == nil || strings.TrimSpace(env.Failure.QueryID) == "" {
		return FailedQuery{}, false
	}
	return FailedQuery{
		FailedQueryID: env.Failure.QueryID,
		ToolName:      strings.TrimSpace(toolName),
		Status:        env.Status,
		Summary:       env.Summary,
	}, true
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
	for _, observation := range observations {
		observation.Kind = strings.TrimSpace(observation.Kind)
		if ValidObservation(observation) {
			out = append(out, observation)
		}
	}
	return out
}

func dataStatusFor(data any) string {
	if isEmptyData(data) {
		return DataEmpty
	}
	return DataPresent
}

func validDataStatus(status string) bool {
	switch status {
	case DataPresent, DataEmpty, DataUnknown, DataPartial:
		return true
	default:
		return false
	}
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
