package contextbudget

import (
	"context"
	"sync"
)

type toolBudgetKey struct{}
type toolBudget struct {
	mu        sync.Mutex
	remaining int
	checks    map[string]bool
	limits    ToolAllocation
}

func WithToolBudget(ctx context.Context) context.Context {
	return WithToolAllocation(ctx, ToolLimits(ctx))
}

func WithToolAllocation(ctx context.Context, limits ToolAllocation) context.Context {
	if _, ok := ctx.Value(toolBudgetKey{}).(*toolBudget); ok {
		return ctx
	}
	limits.TotalTokens = max(0, limits.TotalTokens)
	limits.OutputTokens = min(max(0, limits.OutputTokens), limits.TotalTokens)
	return context.WithValue(ctx, toolBudgetKey{}, &toolBudget{remaining: limits.TotalTokens, limits: limits, checks: make(map[string]bool)})
}

func BeginLogCheck(ctx context.Context, key string) bool {
	b, ok := ctx.Value(toolBudgetKey{}).(*toolBudget)
	if !ok {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.checks[key] || len(b.checks) >= 8 {
		return false
	}
	b.checks[key] = true
	return true
}

// ReserveToolOutput reserves before concurrent backend work, then refunds unused tokens.
func ReserveToolOutput(ctx context.Context, tokens int) (func(int), bool) {
	if tokens <= 0 {
		return nil, false
	}
	b, ok := ctx.Value(toolBudgetKey{}).(*toolBudget)
	if !ok {
		return func(int) {}, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining < tokens {
		return nil, false
	}
	b.remaining -= tokens
	var once sync.Once
	return func(used int) {
		once.Do(func() { b.mu.Lock(); defer b.mu.Unlock(); b.remaining += tokens - max(0, used) })
	}, true
}
