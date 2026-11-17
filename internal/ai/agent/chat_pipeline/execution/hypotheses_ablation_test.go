package execution

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestDiagnosticAblationInstructionsAndGates(t *testing.T) {
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() {
		g.Cfg().SetAdapter(previous)
	})
	for _, variant := range []string{"full", "free_query", "free_submit", "single_hypothesis", "no_feedback", "no_retention"} {
		t.Run(variant, func(t *testing.T) {
			adapter, err := gcfg.NewAdapterContent("agent:\n  diagnostic_ablation: " + variant + "\n")
			if err != nil {
				t.Fatal(err)
			}
			g.Cfg().SetAdapter(adapter)
			ctx := runstate.WithState(t.Context())
			runstate.EnableDiagnostic(ctx)
			if runstate.DiagnosticReady(ctx) != (variant == "free_submit") {
				t.Fatal("submission gate does not match configured ablation")
			}
			proposal, err := diagnosticProposalTool(ctx)
			if err != nil {
				t.Fatal(err)
			}
			info, err := proposal.Info(ctx)
			if err != nil {
				t.Fatal(err)
			}
			parameters, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				t.Fatal(err)
			}
			candidates, ok := parameters.Properties.Get("candidates")
			if !ok {
				t.Fatal("missing proposal candidates schema")
			}
			instruction := diagnosticInstructions(ctx)
			if variant == "single_hypothesis" {
				if !strings.Contains(instruction, "exactly ONE explanation") || !strings.Contains(candidates.Description, "Exactly one") {
					t.Fatal("single-hypothesis instruction and schema disagree")
				}
			}
			if variant == "free_query" && strings.Contains(instruction, "further external queries must match") {
				t.Fatal("free query still requires registering every query")
			}
			if variant == "free_submit" && strings.Contains(instruction, "pending check cannot be skipped") {
				t.Fatal("free submission still forbids pending checks")
			}
		})
	}
}
