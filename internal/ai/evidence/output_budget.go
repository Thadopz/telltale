package evidence

import (
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
)

const ToolOutputTokens = contextbudget.DefaultToolOutputTokens

// BoundOutput projects before local signing; it never reuses an old attestation.
// Unknown payloads cannot safely be summarized as complete statistics.
func BoundOutput(env Envelope) Envelope {
	return BoundOutputWithLimit(env, ToolOutputTokens)
}

func BoundOutputWithLimit(env Envelope, outputTokens int) Envelope {
	contentTokens := max(0, outputTokens-contextbudget.ToolSigningReserve)
	if contextbudget.EstimateTextTokens(Marshal(env)) <= contentTokens {
		return env
	}
	if env.Status != StatusSuccess {
		return NewFailure(StatusError, "RESULT_BUDGET_EXCEEDED", "tool result exceeded model budget", "Result omitted; stop expanding this query.")
	}
	encoded, _ := json.Marshal(env.Data)
	var telemetryData map[string]any
	if json.Unmarshal(encoded, &telemetryData) == nil {
		if metadata, ok := telemetryData["telemetry"].(map[string]any); ok {
			// Keep locators intact. Removing part of a page would skip unseen rows.
			telemetryData["records"] = []any{}
			metadata["returned"] = 0
			metadata["truncated"] = true
			metadata["reason"] = "model_output_budget; no records delivered; retry cursor restarts this page, reduce limit or page_runes"
			metadata["next_cursor"] = metadata["retry_cursor"]
			env.Data = telemetryData
			env.DataStatus = DataPartial
			env.Summary = "Telemetry records omitted by output budget; scope and continuation remain intact."
			if env.Evidence != nil {
				record := *env.Evidence
				record.Observations = nil
				record.ResultMetadata.Truncated = true
				record.ResultMetadata.TruncationReason = "model_output_budget"
				env.Evidence = &record
			}
			if contextbudget.EstimateTextTokens(Marshal(env)) <= contentTokens {
				return env
			}
			return NewFailure(StatusError, "RESULT_BUDGET_EXCEEDED", "telemetry scope and continuation exceed budget", "Narrow query; no records or continuation delivered.")
		}
	}
	for rows := 10; rows > 0; rows /= 2 {
		candidate := env
		encoded, _ := json.Marshal(env.Data)
		var data any
		_ = json.Unmarshal(encoded, &data)
		candidate.Data = boundedValue(data, rows, 256, 0)
		candidate.Summary = "Limited tool projection; statistics and sample coverage may be incomplete. Omitted content cannot refute an anomaly."
		candidate.DataStatus = DataPartial
		if env.Evidence != nil {
			record := *env.Evidence
			record.Attestation = Attestation{}
			// Fields may carry full raw payloads; keep only observations that fit intact.
			record.Observations = nil
			for _, obs := range env.Evidence.Observations {
				encoded, _ := json.Marshal(obs)
				if len(record.Observations) < rows && contextbudget.EstimateTextTokens(string(encoded)) <= 200 {
					record.Observations = append(record.Observations, obs)
				}
			}
			if len(record.Observations) == 0 {
				record.Observations = []Observation{{Kind: ObservationMCPToolResult, Summary: "Payload projected; no intact domain observation fits the budget."}}
			}
			record.ResultMetadata = ResultMetadata{Truncated: true, TruncationReason: "model_output_budget", MaxRows: rows, MaxBytes: 6400, Projection: []string{"bounded_payload", "intact_observations"}}
			payload, _ := json.Marshal(candidate.Data)
			record.ResultMetadata.ReturnedBytes = len(payload)
			candidate.Evidence = &record
		}
		if contextbudget.EstimateTextTokens(Marshal(candidate)) <= contentTokens {
			return candidate
		}
	}
	return NewFailure(StatusError, "RESULT_BUDGET_EXCEEDED", "minimum projection exceeds budget", "Result omitted; stop expanding this query. Backend aggregation or narrower scope is required.")
}

func boundedValue(value any, rows, chars, depth int) any {
	if depth > 8 {
		return "[omitted: depth limit]"
	}
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		if len(runes) > chars {
			return string(runes[:chars]) + " [omitted]"
		}
	case []any:
		out := make([]any, 0, min(rows, len(v)))
		for _, child := range v[:min(rows, len(v))] {
			out = append(out, boundedValue(child, rows, chars, depth+1))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if key == "statistics_coverage" || key == "baseline_coverage" {
				out[key] = "unknown_after_projection"
				continue
			}
			out[key] = boundedValue(child, rows, chars, depth+1)
		}
		return out
	}
	return value
}
