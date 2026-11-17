package models

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/cloudwego/eino/schema"
)

func TestFinalRequestGuardUsesAllocatedToolLimits(t *testing.T) {
	m := &requestBudgetModel{profile: contextbudget.ModelContextProfile{ModelName: "budget-test", ContextWindowTokens: 32768, MaxOutputTokens: 4096, WorkingPromptCap: 32768}}
	ctx := contextbudget.WithToolAllocation(t.Context(), contextbudget.ToolAllocation{TotalTokens: 2000, OutputTokens: 1000})
	message := &schema.Message{Role: schema.Tool, ToolName: "query_internal_docs", Content: strings.Repeat("x", 5000)}
	if err := m.check(ctx, []*schema.Message{message}, nil); err == nil || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("smaller output limit not enforced: %v", err)
	}
	message.Content = strings.Repeat("x", 3000)
	if err := m.check(ctx, []*schema.Message{message}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.check(ctx, []*schema.Message{message, message, message}, nil); err == nil || !strings.Contains(err.Error(), "2000") {
		t.Fatalf("shared limit not enforced: %v", err)
	}
}
