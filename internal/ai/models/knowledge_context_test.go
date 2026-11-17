package models

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/schema"
)

func TestKnowledgeFollowUpRetainsEarlierEvidenceAndConflictingVersions(t *testing.T) {
	var messages []*schema.Message
	for i, content := range []string{
		"Version 1: retry callbacks immediately.",
		"Version 2 supersedes version 1: verify idempotency before retrying callbacks.",
	} {
		env := evidence.NewDataWithObservations(content, "retrieved documentation", []evidence.Observation{{
			Kind:   evidence.ObservationRunbookRule,
			Fields: map[string]any{"document_id": "callback-runbook", "runbook_rule": content},
		}})
		callID := string(rune('a' + i))
		messages = append(messages,
			schema.AssistantMessage("", []schema.ToolCall{{ID: callID, Function: schema.FunctionCall{Name: "query_internal_docs"}}}),
			&schema.Message{Role: schema.Tool, ToolName: "query_internal_docs", ToolCallID: callID, Content: evidence.Marshal(env)},
		)
	}
	original := messages[1].Content
	views := consumedToolViews(messages)
	if !strings.Contains(views[1].Content, "retry callbacks immediately") || !strings.Contains(views[3].Content, "verify idempotency") {
		t.Fatal("follow-up retrieval lost one side of the document conflict")
	}
	if !strings.Contains(views[1].Content, "callback-runbook") || !strings.Contains(views[1].Content, "retained_document_evidence") {
		t.Fatal("earlier document evidence cannot be traced")
	}
	if messages[1].Content != original || views[1].ToolCallID != messages[1].ToolCallID {
		t.Fatal("compaction changed the audit record or tool pairing")
	}
	if twice := consumedToolViews(views); twice[1].Content != views[1].Content {
		t.Fatal("repeated model calls changed retained knowledge evidence")
	}
}
