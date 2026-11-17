package diagnostic

import (
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
)

func TestLogStateRetainsExpiringReferencesAndCoverage(t *testing.T) {
	state := BuildDiagnosticState("log incident")
	env := evidence.NewDataWithObservations(nil, "partial samples", []evidence.Observation{{Kind: evidence.ObservationMCPToolResult, Fields: map[string]any{"query_ref": "log_query", "sample_references": []any{"log_sample"}}}})
	items := []trace.ToolEvidence{{ToolName: "analyze_log_window", InputJSON: `{"service":"payment"}`, Envelope: env}}
	UpdateDiagnosticState(&state, items)
	prompt := diagnosticControlPrompt(state, items)
	for _, want := range []string{"log_sample", "no_trace_backend", "completed_log_check_do_not_repeat", "refs_expire_10m_or_restart"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing log state %s", want)
		}
	}
}

func TestNonPaymentLogStateWithCustomHypothesisKeepsSourceAndStatistics(t *testing.T) {
	state := DiagnosticState{Round: 1, MaxRounds: 2, Hypotheses: []Hypothesis{{ID: "inventory_health", Status: HypothesisOpen}}}
	env := evidence.NewDataWithObservations(map[string]any{"source_id": "inventory_logs", "service": "inventory", "count_unit": "log_event", "start": "2026-10-14T10:00:00Z", "end": "2026-10-14T10:10:00Z", "totals": map[string]any{"events": 100000}, "candidates": []any{map[string]any{"operation": "shared", "event_error_share_delta": 0.2}}, "samples": []any{map[string]any{"operation": "shared", "level": "INFO", "reference": "log_control"}}, "capabilities": map[string]any{"read": true}}, "window", []evidence.Observation{{Kind: evidence.ObservationMCPToolResult, Subject: evidence.SubjectRef{Service: "inventory"}, Fields: map[string]any{"query_ref": "log_query", "sample_references": []any{"log_error", "log_control"}}}})
	UpdateDiagnosticState(&state, []trace.ToolEvidence{{ToolName: "analyze_log_window", InputJSON: `{"source_id":"inventory_logs","service":"inventory"}`, Envelope: env}})
	prompt := diagnosticControlPrompt(state, nil)
	for _, want := range []string{"inventory_logs", "inventory", "100000", "0.2", "log_control", "log_error", "capabilities"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("non-payment log state lost %s", want)
		}
	}
	if len(state.Hypotheses[0].LogWindows) != 1 {
		t.Fatal("window state not retained beside custom hypothesis")
	}
}
