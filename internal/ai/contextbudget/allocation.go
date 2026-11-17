package contextbudget

import (
	"context"
	"math"
)

const DefaultToolOutputTokens = 2000
const DefaultToolTotalTokens = 4 * DefaultToolOutputTokens
const ToolSigningReserve = 400
const DefaultSelectionTarget = (DefaultToolOutputTokens - ToolSigningReserve) * 3 / 4
const MaxSelectorInputTokens = 12000

type ToolAllocation struct {
	TotalTokens  int
	OutputTokens int
}

// AllocateTools reserves a quarter of usable input, within the standalone ceilings.
func AllocateTools(usableTokens int) ToolAllocation {
	total := min(DefaultToolTotalTokens, max(0, usableTokens/4))
	return ToolAllocation{TotalTokens: total, OutputTokens: min(DefaultToolOutputTokens, total)}
}

func (a ToolAllocation) ContentTokens() int   { return max(0, a.OutputTokens-ToolSigningReserve) }
func (a ToolAllocation) SelectionTarget() int { return a.ContentTokens() * 3 / 4 }

func ToolLimits(ctx context.Context) ToolAllocation {
	if b, ok := ctx.Value(toolBudgetKey{}).(*toolBudget); ok {
		return b.limits
	}
	return ToolAllocation{TotalTokens: DefaultToolTotalTokens, OutputTokens: DefaultToolOutputTokens}
}

// SelectorInputLimit belongs to a separate model call, not to the answer's output allocation.
func SelectorInputLimit(ctx context.Context, outputTokens int) (int, error) {
	profile := ConfiguredProfile(ctx, "quick_chat_model")
	profile.MaxOutputTokens = max(profile.MaxOutputTokens, outputTokens)
	factor := DefaultCalibrator().Factor(ctx, CalibrationKey(profile))
	budget, err := Calculate(profile, 0, 0, factor)
	if err != nil {
		return 0, err
	}
	return min(MaxSelectorInputTokens, int(math.Floor(float64(budget.ContentBudgetTokens)/factor))), nil
}
