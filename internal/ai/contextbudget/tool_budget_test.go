package contextbudget

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentToolBudgetIsRequestLocal(t *testing.T) {
	ctx := WithToolBudget(t.Context())
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := ReserveToolOutput(ctx, 2000); ok {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 4 {
		t.Fatalf("concurrent reservations=%d want 4", accepted.Load())
	}
	if _, ok := ReserveToolOutput(WithToolBudget(t.Context()), 2000); !ok {
		t.Fatal("budget leaked across requests")
	}
}

func TestToolBudgetRefundsOnlyUnusedReservation(t *testing.T) {
	ctx := WithToolBudget(t.Context())
	finish, ok := ReserveToolOutput(ctx, 2000)
	if !ok {
		t.Fatal("initial reservation denied")
	}
	finish(500)
	if _, ok := ReserveToolOutput(ctx, 7500); !ok {
		t.Fatal("unused reservation was not released")
	}
	if _, ok := ReserveToolOutput(ctx, 1); ok {
		t.Fatal("budget exceeded")
	}
}
