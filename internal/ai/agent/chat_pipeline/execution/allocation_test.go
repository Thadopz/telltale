package execution

import (
	"math"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
)

func TestHistoryAndToolsShareTheSameModelBudget(t *testing.T) {
	for _, window := range []int{8192, 32768, 131072} {
		plan := contextBudgetPlan{Profile: contextbudget.ModelContextProfile{ModelName: "allocation-test", ContextWindowTokens: window, MaxOutputTokens: 2048, WorkingPromptCap: window, MinSafetyMarginTokens: 512, SafetyMarginRatio: 0.1}, PromptOverhead: 1000}
		budget, limits, err := plan.allocations(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		full, err := contextbudget.Calculate(plan.Profile, plan.PromptOverhead, 0, budget.CalibrationFactor)
		if err != nil {
			t.Fatal(err)
		}
		if budget.ContentBudgetTokens+int(math.Ceil(float64(limits.TotalTokens)*budget.CalibrationFactor)) != full.ContentBudgetTokens {
			t.Fatal("history did not reserve tool capacity")
		}
		ctx, err := plan.withToolBudget(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if contextbudget.ToolLimits(ctx) != limits {
			t.Fatal("runtime differs from assembly allocation")
		}
		got, err := budgetModelContextWithPlan(ctx, ModelContext{Content: "current question"}, plan)
		if err != nil {
			t.Fatal(err)
		}
		if got.Budget.ToolReserveTokens != limits.TotalTokens || got.Budget.KnowledgeTargetTokens != limits.SelectionTarget() {
			t.Fatal("budget report lost allocation")
		}
	}
}
