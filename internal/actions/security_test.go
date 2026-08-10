package actions

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfirmedActionCanOnlyBeConsumedOnceConcurrently(t *testing.T) {
	actionStore := &memoryActionStore{
		actions: map[string]pendingAction{
			"one-time-token": {
				PreparedAction: PreparedAction{
					Token:     "one-time-token",
					ExpiresAt: time.Now().Add(time.Minute),
				},
				Confirmed: true,
			},
		},
	}

	const workers = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			if err := actionStore.ConsumeConfirmed(context.Background(), "one-time-token", time.Now()); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	waitGroup.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly one token consumer, got %d", successes.Load())
	}
}

func TestCleanToolRequestRejectsMalformedAndOversizedArguments(t *testing.T) {
	if _, _, err := cleanToolRequest("mysql_crud", `{"sql":`); err == nil {
		t.Fatal("expected malformed JSON to be rejected")
	}
	oversized := `{"value":"` + strings.Repeat("x", maxActionArgumentsBytes) + `"}`
	if _, _, err := cleanToolRequest("mysql_crud", oversized); err == nil {
		t.Fatal("expected oversized action arguments to be rejected")
	}
	if _, _, err := cleanToolRequest("mysql_crud\nforged", `{}`); err == nil {
		t.Fatal("expected unsafe tool name to be rejected")
	}
}
