package chat

import (
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/utility/mem"
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNormalizeKnowledgeMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    chat_pipeline.KnowledgeMode
		wantErr bool
	}{
		{name: "empty defaults to auto", input: "", want: chat_pipeline.KnowledgeAuto},
		{name: "normalizes case and spaces", input: " ALWAYS ", want: chat_pipeline.KnowledgeAlways},
		{name: "accepts never", input: "never", want: chat_pipeline.KnowledgeNever},
		{name: "rejects unknown mode", input: "sometimes", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeKnowledgeMode(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize knowledge mode: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestPrepareChatTurnRejectsEmptyQuestion(t *testing.T) {
	_, err := prepareChatTurn(context.Background(), "conversation-1", "", "", "  ", "")
	if err == nil {
		t.Fatal("expected empty question to be rejected")
	}
}

func TestPrepareChatTurnRejectsMissingMemoryScope(t *testing.T) {
	_, err := prepareChatTurn(context.Background(), "", "", "", "hello", "")
	if err == nil {
		t.Fatal("expected missing user and conversation ids to be rejected")
	}
}

func TestAppendCompletedMessagesUsesAssistantRole(t *testing.T) {
	memory := &mem.SimpleMemory{}

	appendCompletedMessages(memory, "question", "answer")

	messages := memory.GetMessages()
	if len(messages) != 2 {
		t.Fatalf("expected two messages, got %d", len(messages))
	}
	if messages[0].Role != schema.User {
		t.Fatalf("expected user role, got %q", messages[0].Role)
	}
	if messages[1].Role != schema.Assistant {
		t.Fatalf("expected assistant role, got %q", messages[1].Role)
	}
}
