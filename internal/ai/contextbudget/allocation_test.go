package contextbudget

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"testing"
)

func TestToolAllocationScalesAndRetainsSigningSpace(t *testing.T) {
	for _, available := range []int{0, 1000, 4096, 32768, 1000000} {
		limits := AllocateTools(available)
		if limits.TotalTokens > available/4 || limits.TotalTokens > DefaultToolTotalTokens || limits.OutputTokens > limits.TotalTokens || limits.OutputTokens > DefaultToolOutputTokens {
			t.Fatalf("invalid allocation for %d: %+v", available, limits)
		}
		if limits.SelectionTarget() > limits.ContentTokens() || limits.ContentTokens() > max(0, limits.OutputTokens-ToolSigningReserve) {
			t.Fatalf("signing space lost: %+v", limits)
		}
	}
	ctx := WithToolAllocation(t.Context(), AllocateTools(4096))
	if got := ToolLimits(ctx); got.TotalTokens != 1024 || got.OutputTokens != 1024 || got.SelectionTarget() != 468 {
		t.Fatalf("unexpected small-window allocation: %+v", got)
	}
	child := WithToolAllocation(ctx, AllocateTools(1000000))
	if ToolLimits(child) != ToolLimits(ctx) {
		t.Fatal("delegation reset shared budget")
	}
}

func TestToolSettlementIsIdempotentAndChargesOverruns(t *testing.T) {
	ctx := WithToolAllocation(t.Context(), ToolAllocation{TotalTokens: 1000, OutputTokens: 1000})
	finish, ok := ReserveToolOutput(ctx, 1000)
	if !ok {
		t.Fatal("reservation failed")
	}
	finish(700)
	finish(0)
	if _, ok := ReserveToolOutput(ctx, 301); ok {
		t.Fatal("duplicate settlement minted tokens")
	}
	if _, ok := ReserveToolOutput(ctx, 300); !ok {
		t.Fatal("unused tokens not refunded")
	}
	ctx = WithToolAllocation(t.Context(), ToolAllocation{TotalTokens: 1000, OutputTokens: 1000})
	finish, _ = ReserveToolOutput(ctx, 500)
	finish(1100)
	if _, ok := ReserveToolOutput(ctx, 1); ok {
		t.Fatal("overrun was not charged")
	}
	if _, ok := ReserveToolOutput(ctx, -1); ok {
		t.Fatal("negative reservation accepted")
	}
}

func TestSelectorInputFitsItsOwnModel(t *testing.T) {
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	adapter, err := gcfg.NewAdapterContent(`quick_chat_model:
  model: selector-budget-test
  context_window_tokens: 8192
  max_output_tokens: 4096
context:
  min_safety_margin_tokens: 2048
`)
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	limit, err := SelectorInputLimit(t.Context(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if limit != 2048 {
		t.Fatalf("selector input=%d want 2048", limit)
	}
	if _, err := SelectorInputLimit(t.Context(), 8192); err == nil {
		t.Fatal("exhausted selector window accepted")
	}
}
