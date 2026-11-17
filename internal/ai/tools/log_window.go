package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"net/url"
	"strings"
	"sync"
	"time"
)

type AnalyzeLogWindowInput struct {
	SourceID      string `json:"source_id,omitempty" jsonschema:"description=Configured diagnostic HTTP source identifier; never a URL"`
	Service       string `json:"service" jsonschema:"description=Explicit service allowed by the selected source and current permissions"`
	Start         string `json:"start" jsonschema:"description=RFC3339 start with timezone; inclusive"`
	End           string `json:"end" jsonschema:"description=RFC3339 end with timezone; exclusive; at most one hour"`
	BaselineStart string `json:"baseline_start,omitempty"`
	BaselineEnd   string `json:"baseline_end,omitempty"`
	Operation     string `json:"operation,omitempty" jsonschema:"description=Optional exact operation from candidates for one conditional drilldown"`
}

type ReadLogEvidenceInput struct {
	Reference string `json:"reference" jsonschema:"description=Opaque log reference issued by analyze_log_window; never a path or URL"`
}

type logReference struct {
	User, Tenant, Conversation       string
	Service, Start, End              string
	SourceID, SourceBinding, Locator string
	Expires                          time.Time
	Digest                           string
}

// shortcut: references live for ten minutes in this process, use shared trusted storage for multi-instance routing.
var logReferences = struct {
	sync.Mutex
	entries map[string]logReference
}{entries: make(map[string]logReference)}

func storeLogReference(ctx context.Context, ref logReference) (string, error) {
	principal := authz.PrincipalFromContext(ctx)
	if principal.UserID == "" {
		return "", fmt.Errorf("authenticated user required for log references")
	}
	ref.User, ref.Tenant, ref.Conversation = principal.UserID, principal.TenantID, contextString(ctx, "client_id")
	ref.Expires = time.Now().Add(10 * time.Minute)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	key := "log_" + hex.EncodeToString(nonce[:])
	logReferences.Lock()
	defer logReferences.Unlock()
	for key, value := range logReferences.entries {
		if time.Now().After(value.Expires) {
			delete(logReferences.entries, key)
		}
	}
	if len(logReferences.entries) >= 1024 {
		return "", fmt.Errorf("log reference capacity exhausted")
	}
	logReferences.entries[key] = ref
	return key, nil
}

func resolveLogReference(ctx context.Context, key string) (logReference, error) {
	logReferences.Lock()
	defer logReferences.Unlock()
	ref, ok := logReferences.entries[key]
	p := authz.PrincipalFromContext(ctx)
	if !ok || time.Now().After(ref.Expires) || p.UserID == "" || ref.User != p.UserID || ref.Tenant != p.TenantID || ref.Conversation != contextString(ctx, "client_id") {
		return logReference{}, fmt.Errorf("log reference unavailable, expired, or outside authorized scope")
	}
	return ref, nil
}

func validateLogWindow(input *AnalyzeLogWindowInput) error {
	if input == nil || strings.TrimSpace(input.Service) == "" || len(input.Service) > 64 || len(input.Operation) > 128 {
		return fmt.Errorf("explicit service and bounded operation filter required")
	}
	start, err := time.Parse(time.RFC3339, input.Start)
	if err != nil {
		return fmt.Errorf("invalid start timezone: %w", err)
	}
	end, err := time.Parse(time.RFC3339, input.End)
	if err != nil || !end.After(start) || end.Sub(start) > time.Hour {
		return fmt.Errorf("invalid window: use RFC3339 and at most one hour")
	}
	if input.BaselineStart != "" || input.BaselineEnd != "" {
		baseStart, e1 := time.Parse(time.RFC3339, input.BaselineStart)
		baseEnd, e2 := time.Parse(time.RFC3339, input.BaselineEnd)
		if e1 != nil || e2 != nil || baseEnd.Sub(baseStart) != end.Sub(start) || baseEnd.After(start) {
			return fmt.Errorf("baseline must be earlier, non-overlapping and equal length")
		}
	}
	return nil
}

func logSampleDigest(sample map[string]any) string {
	message, _ := sample["message"].(string)
	runes := []rune(message)
	if len(runes) > 160 {
		message = string(runes[:160])
	}
	value := map[string]any{"id": sample["id"], "message": message, "level": sample["level"], "operation": sample["operation"], "occurred_at": sample["occurred_at"]}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func NewAnalyzeLogWindowTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool("analyze_log_window", "Compare configured, authorized source/service log-event windows on the server; returns capabilities, candidates, finite ERROR/INFO controls and available opaque references. No request denominator or trace backend. Use one operation drilldown and stop on repeated checks or missing capability.",
		func(ctx context.Context, input *AnalyzeLogWindowInput, opts ...tool.Option) (string, error) {
			if err := validateLogWindow(input); err != nil {
				return marshalToolError(err, "invalid log scope"), nil
			}
			sourceID, source, err := resolveLogSource(ctx, input.SourceID, input.Service)
			if err != nil {
				return marshalToolError(err, "log source authorization or configuration unavailable"), nil
			}
			if !source.Capabilities.Aggregation || (input.Operation != "" && !source.Capabilities.Operation) {
				return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "CAPABILITY_NOT_SUPPORTED", "source lacks aggregation or operation filtering", "Stop expansion; no raw-log aggregation fallback.")), nil
			}
			input.SourceID = sourceID
			for _, field := range []*string{&input.Start, &input.End, &input.BaselineStart, &input.BaselineEnd} {
				if *field != "" {
					instant, _ := time.Parse(time.RFC3339, *field)
					*field = instant.UTC().Format(time.RFC3339Nano)
				}
			}
			if input.BaselineStart == "" && source.Capabilities.Baseline {
				start, _ := time.Parse(time.RFC3339, input.Start)
				end, _ := time.Parse(time.RFC3339, input.End)
				input.BaselineStart = start.Add(-end.Sub(start)).Format(time.RFC3339Nano)
				input.BaselineEnd = input.Start
			}
			canonical, _ := json.Marshal(input)
			if !contextbudget.BeginLogCheck(ctx, "analyze_log_window\n"+string(canonical)) {
				return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "LOG_CHECK_ALREADY_USED", "normalized source/window check already used", "Stop expansion; retain existing evidence.")), nil
			}
			values := url.Values{"service": {input.Service}, "start": {input.Start}, "end": {input.End}, "baseline_start": {input.BaselineStart}, "baseline_end": {input.BaselineEnd}, "operation": {input.Operation}}
			if !source.Capabilities.Baseline {
				values.Del("baseline_start")
				values.Del("baseline_end")
			}
			data, err := source.query(ctx, "/api/v1/agent/analyze_log_window", values)
			if err != nil {
				return marshalToolError(err, "Log aggregation unavailable; report source capability gap without querying another source."), nil
			}
			backendStart, e1 := time.Parse(time.RFC3339, stringField(data, "start"))
			backendEnd, e2 := time.Parse(time.RFC3339, stringField(data, "end"))
			requestedStart, _ := time.Parse(time.RFC3339, input.Start)
			requestedEnd, _ := time.Parse(time.RFC3339, input.End)
			if data["service"] != input.Service || data["count_unit"] != "log_event" || e1 != nil || e2 != nil || !backendStart.Equal(requestedStart) || !backendEnd.Equal(requestedEnd) || stringField(data, "operation") != input.Operation {
				return marshalToolError(nil, "backend log scope or count unit mismatch"), nil
			}
			if source.Capabilities.Baseline {
				baseStart, err1 := time.Parse(time.RFC3339, stringField(data, "baseline_start"))
				baseEnd, err2 := time.Parse(time.RFC3339, stringField(data, "baseline_end"))
				requestedBaseStart, _ := time.Parse(time.RFC3339, input.BaselineStart)
				requestedBaseEnd, _ := time.Parse(time.RFC3339, input.BaselineEnd)
				if err1 != nil || err2 != nil || !baseStart.Equal(requestedBaseStart) || !baseEnd.Equal(requestedBaseEnd) {
					return marshalToolError(nil, "backend baseline window scope mismatch"), nil
				}
			}
			data["source_id"], data["capabilities"] = sourceID, source.Capabilities
			data["limitations"] = append(anySlice(data["limitations"]), "Reference tenant binding does not provide database tenant isolation.")
			if !source.Capabilities.Baseline {
				data["baseline_totals"], data["baseline_start"], data["baseline_end"], data["baseline_reason"], data["baseline_coverage"] = nil, nil, nil, "capability unavailable", "unavailable"
				data["limitations"] = append(anySlice(data["limitations"]), "Baseline comparison unavailable.")
				for _, value := range anySlice(data["candidates"]) {
					if candidate, ok := value.(map[string]any); ok {
						delete(candidate, "baseline")
						delete(candidate, "event_error_share_delta")
						candidate["comparable"] = false
					}
				}
			}
			samples, _ := data["samples"].([]any)
			for _, value := range samples {
				sample, ok := value.(map[string]any)
				if !ok {
					return marshalToolError(nil, "invalid log sample"), nil
				}
				if service, ok := sample["service"].(string); ok && service != input.Service {
					return marshalToolError(nil, "backend sample service mismatch"), nil
				}
				delete(sample, "reference")
				if source.Capabilities.Read {
					locator, err := source.locator(sample)
					if err != nil {
						return marshalToolError(err, "invalid backend sample locator"), nil
					}
					ref, err := storeLogReference(ctx, logReference{SourceID: sourceID, SourceBinding: source.binding(), Service: input.Service, Start: input.Start, End: input.End, Locator: locator, Digest: logSampleDigest(sample)})
					if err != nil {
						return marshalToolError(err, "cannot issue log reference"), nil
					}
					sample["reference"] = ref
				}
				if message, ok := sample["message"].(string); ok {
					runes := []rune(message)
					if len(runes) > 64 {
						sample["message"] = string(runes[:64]) + " [excerpt]"
					}
				}
				delete(sample, "id")
			}
			if !source.Capabilities.Read {
				data["limitations"] = append(anySlice(data["limitations"]), "Detail retrieval unavailable; no sample references issued.")
			}
			queryRef, err := storeLogReference(ctx, logReference{SourceID: sourceID, SourceBinding: source.binding(), Service: input.Service, Start: input.Start, End: input.End})
			if err != nil {
				return marshalToolError(err, "cannot issue query reference"), nil
			}
			data["query_ref"] = queryRef
			data["queried_at"] = time.Now().UTC().Format(time.RFC3339)
			data["limitations"] = append(anySlice(data["limitations"]), "Opaque references expire in ten minutes or on process restart; query_ref locates scope only, read a sample reference for detail.")
			for {
				env := logWindowEnvelope(data, len(samples))
				if contextbudget.EstimateTextTokens(evidence.Marshal(env)) <= 1550 {
					return evidence.Marshal(env), nil
				}
				if len(samples) == 0 {
					return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "RESULT_BUDGET_EXCEEDED", "minimum log statistics exceed budget", "Stop expansion and report the budget gap.")), nil
				}
				if len(samples) <= 2 {
					return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "RESULT_BUDGET_EXCEEDED", "statistics and control samples exceed budget", "Stop expansion; narrow the operation filter.")), nil
				}
				lastOperation := stringField(samples[len(samples)-1].(map[string]any), "operation")
				for len(samples) > 0 && stringField(samples[len(samples)-1].(map[string]any), "operation") == lastOperation {
					samples = samples[:len(samples)-1]
				}
				data["samples"] = samples
			}
		})
}

func anySlice(value any) []any { values, _ := value.([]any); return values }

func logWindowEnvelope(data map[string]any, rows int) evidence.Envelope {
	payload, _ := json.Marshal(data)
	refs := []any{}
	observations := []evidence.Observation{}
	for _, value := range anySlice(data["samples"]) {
		sample, _ := value.(map[string]any)
		if ref, ok := sample["reference"].(string); ok {
			refs = append(refs, ref)
		}
		if sample["level"] == "ERROR" {
			observations = append(observations, evidence.Observation{Kind: evidence.ObservationLogError, Subject: evidence.SubjectRef{Service: stringField(data, "service"), Fields: map[string]string{"source_id": stringField(data, "source_id")}}, Scope: evidence.EvidenceScope{StartTime: stringField(data, "start"), EndTime: stringField(data, "end")}, Fields: map[string]any{"operation": sample["operation"], "level": "ERROR", "log_reference": sample["reference"]}, Summary: "ERROR log sample within the queried window; no causal claim."})
		}
	}
	observations = append(observations, evidence.Observation{Kind: evidence.ObservationMCPToolResult, Subject: evidence.SubjectRef{Service: stringField(data, "service"), Fields: map[string]string{"source_id": stringField(data, "source_id")}}, Fields: map[string]any{"source_id": data["source_id"], "service": data["service"], "query_ref": data["query_ref"], "sample_references": refs, "count_unit": "log_event", "statistics_coverage": data["statistics_coverage"], "samples_coverage": data["samples_coverage"]}, Summary: "Log events only; finite samples; no trace backend. References expire in ten minutes or on restart."})
	env := evidence.NewDataWithResultMetadata(data, "Log-event comparison; association is not causation; samples partial.", observations, evidence.ResultMetadata{ReturnedRows: rows, MaxRows: 8, ReturnedBytes: len(payload), MaxBytes: 5800, Truncated: true, TruncationReason: "finite_window_samples_and_top_k", Aggregation: "server_side_operation_level", Projection: []string{"statistics", "candidates", "bounded_samples", "references", "limitations"}})
	env.Evidence.Provenance.SourceKind = evidence.SourceRuntimeLog
	env.Evidence.Provenance.RuntimeVerified = true
	return env
}

func NewReadLogEvidenceTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool("read_log_evidence", "Read one bounded original log excerpt using a sample reference from analyze_log_window. Rechecks user/tenant/conversation and expiry; reports changes since sample, never accepts paths, URLs or arbitrary trace IDs.", func(ctx context.Context, input *ReadLogEvidenceInput, opts ...tool.Option) (string, error) {
		if input == nil {
			return marshalToolError(nil, "missing log reference"), nil
		}
		ref, err := resolveLogReference(ctx, strings.TrimSpace(input.Reference))
		if err != nil || ref.Locator == "" {
			return marshalToolError(err, "invalid or expired sample reference"), nil
		}
		sourceID, source, err := resolveLogSource(ctx, ref.SourceID, ref.Service)
		if err != nil {
			return marshalToolError(err, "log reference access revoked or source unavailable"), nil
		}
		if source.binding() != ref.SourceBinding || !source.Capabilities.Read {
			return marshalToolError(nil, "log reference source changed or detail capability revoked"), nil
		}
		key, _ := json.Marshal([]string{sourceID, ref.Service, ref.Start, ref.End, ref.Locator})
		if !contextbudget.BeginLogCheck(ctx, "read_log_evidence\n"+string(key)) {
			return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "LOG_CHECK_ALREADY_USED", "same source detail check already used", "Stop expansion; retain existing evidence.")), nil
		}
		data, err := source.query(ctx, "/api/v1/agent/read_log_evidence", url.Values{"service": {ref.Service}, "id": {ref.Locator}, "start": {ref.Start}, "end": {ref.End}})
		if err != nil {
			return marshalToolError(err, "log detail unavailable or retention expired"), nil
		}
		locator, locatorErr := source.locator(data)
		if locatorErr != nil || locator != ref.Locator || data["service"] != ref.Service {
			return marshalToolError(nil, "backend log detail scope mismatch"), nil
		}
		data["changed_since_sample"] = logSampleDigest(data) != ref.Digest
		delete(data, "id")
		data["reference"] = input.Reference
		data["source_id"], data["capabilities"] = sourceID, source.Capabilities
		data["limitations"] = []string{"Bounded original excerpt only; trace ID does not imply trace retrieval capability. Changes compare the original sample fields only, not a frozen full-log snapshot."}
		return evidence.Marshal(evidence.NewDataWithResultMetadata(data, "Original log excerpt; check changed_since_sample and truncation.", []evidence.Observation{{Kind: evidence.ObservationMCPToolResult, Subject: evidence.SubjectRef{Service: ref.Service, Fields: map[string]string{"source_id": sourceID}}, Scope: evidence.EvidenceScope{StartTime: ref.Start, EndTime: ref.End}, Fields: map[string]any{"source_id": sourceID, "service": ref.Service, "log_reference": input.Reference}, Summary: "Bounded log excerpt; no root cause asserted."}}, evidence.ResultMetadata{ReturnedRows: 1, MaxRows: 1, Truncated: true, TruncationReason: "bounded_original_excerpt", Projection: []string{"message", "error_message", "operation", "level", "occurred_at", "trace_id", "reference"}})), nil
	})
}
