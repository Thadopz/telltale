package diagnostic

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline/execution"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestDisabledHypothesesRemoveControlAndFollowUp(t *testing.T) {
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() {
		g.Cfg().SetAdapter(previous)
	})
	adapter, err := gcfg.NewAdapterContent("agent:\n  diagnostic_hypotheses: false\n")
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	state := BuildDiagnosticState("Investigate a service failure")
	SyncDiagnosticHypotheses(t.Context(), &state)
	if len(state.Hypotheses) != 0 || NeedsDiagnosticFollowUp(state) {
		t.Fatal("disabled diagnosis retains hypotheses or requests hypothesis follow-up")
	}
	input := &execution.UserMessage{Query: "Investigate a service failure"}
	turn := PrepareDiagnosticTurn(input, state)
	if strings.TrimSpace(turn.Query) != input.Query {
		t.Fatalf("disabled diagnosis added control instructions: %s", turn.Query)
	}
	state.RequiredChecks = []DiagnosticCheck{{ID: "incident-observation", ToolName: "query_prometheus_alerts"}}
	turn = PrepareDiagnosticTurn(input, state)
	if !strings.Contains(turn.Query, "query_prometheus_alerts") || strings.Contains(turn.Query, "working hypotheses") {
		t.Fatal("required incident observation lost or hypothesis prompt retained")
	}
}

func TestNoRetentionOmitsHypothesesFromTurn(t *testing.T) {
	state := BuildDiagnosticState("Investigate a service failure")
	state.SuppressHypothesisContext = true
	input := &execution.UserMessage{Query: "Investigate a service failure"}
	turn := PrepareDiagnosticTurn(input, state)
	if strings.TrimSpace(turn.Query) != input.Query {
		t.Fatalf("hypothesis context retained: %s", turn.Query)
	}
	if len(state.Hypotheses) == 0 {
		t.Fatal("internal hypotheses must remain available for execution checks")
	}
}
