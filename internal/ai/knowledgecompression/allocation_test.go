package knowledgecompression

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestSelectionTriggerUsesRequestOutputAllocation(t *testing.T) {
	env := evidence.NewDataWithObservations(nil, "docs", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "doc-1", "runbook_rule": strings.Repeat("条件", 300)}}})
	if NeedsSelection(env) {
		t.Fatal("fixture should fit default budget")
	}
	ctx := contextbudget.WithToolAllocation(t.Context(), contextbudget.ToolAllocation{TotalTokens: 1000, OutputTokens: 1000})
	if !NeedsSelectionInContext(ctx, env) {
		t.Fatal("small request failed to trigger selection")
	}
	if contextbudget.ToolLimits(ctx).SelectionTarget() >= TargetTokens {
		t.Fatal("selection target did not shrink")
	}
}
