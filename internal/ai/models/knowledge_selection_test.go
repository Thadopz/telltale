package models

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/knowledgecompression"
	"github.com/cloudwego/eino/schema"
	"testing"
)

func TestChangedKnowledgeGoalInvalidatesOnlySelectedView(t *testing.T) {
	ctx := knowledgecompression.WithGoal(t.Context(), "新问题", "")
	env := evidence.NewDataWithObservations(nil, "selected", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"runbook_rule": "原文", "document_id": "doc", "selection": map[string]any{"goal_hash": "old"}}}})
	messages := []*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call"}}}, {Role: schema.Tool, ToolCallID: "call", ToolName: "query_internal_docs", Content: evidence.Marshal(env)}}
	views := invalidateChangedKnowledgeGoal(ctx, messages)
	if views[0] != messages[0] || views[1].ToolCallID != "call" || evidence.Parse(views[1].Content).Envelope.Failure.Code != "KNOWLEDGE_GOAL_CHANGED" || messages[1].Content != evidence.Marshal(env) {
		t.Fatal("pairing or original audit changed")
	}
	if again := invalidateChangedKnowledgeGoal(ctx, views); again[1].Content != views[1].Content {
		t.Fatal("invalidation not idempotent")
	}
}
