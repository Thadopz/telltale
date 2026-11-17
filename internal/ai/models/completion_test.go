package models

import (
	"testing"

	"github.com/Thadopz/telltale/internal/ai/runstate"
)

func TestCompletedRunDoesNotRequireProvider(t *testing.T) {
	ctx := runstate.WithState(t.Context())
	runstate.Complete(ctx)
	guard := &requestBudgetModel{}
	message, err := guard.Generate(ctx, nil)
	if err != nil || message.Content != runstate.CompletionMessage {
		t.Fatalf("completed run did not return its completion receipt: %v", err)
	}
	stream, err := guard.Stream(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	message, err = stream.Recv()
	if err != nil || message.Content != runstate.CompletionMessage {
		t.Fatalf("completed stream did not return its completion receipt: %v", err)
	}
}
