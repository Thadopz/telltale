package tools

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestToolProjectionAndSigningUseAllocatedLimit(t *testing.T) {
	env := evidence.NewDataWithObservations(map[string]any{"details": strings.Repeat("detail ", 600)}, "result", []evidence.Observation{{Kind: evidence.ObservationMCPToolResult, Summary: "actual bounded observation"}})
	ctx := contextbudget.WithToolAllocation(t.Context(), contextbudget.ToolAllocation{TotalTokens: 1000, OutputTokens: 1000})
	output := IssueToolOutput(ctx, "allocated-read", evidence.Marshal(env))
	if got := contextbudget.EstimateTextTokens(output); got > 1000 {
		t.Fatalf("signed output=%d exceeds allocation", got)
	}
	parsed := evidence.Parse(output).Envelope
	if parsed.Status != evidence.StatusSuccess {
		t.Fatalf("projection failed: %s", output)
	}
	if parsed.DataStatus != evidence.DataPartial {
		t.Fatal("projection did not disclose partial coverage")
	}
	if _, ok := parsed.UsableEvidence(); !ok {
		t.Fatal("projection lost local signature")
	}
}
