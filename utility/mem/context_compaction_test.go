package mem

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

func TestCompactionPreservesExactDetailsInCrowdedHistory(t *testing.T) {
	memory := &SimpleMemory{
		ID:            "crowded-context",
		MaxWindowSize: 50,
		Policy: CompactionPolicy{
			MaxTokens:         120,
			TriggerRatio:      0.70,
			TargetRatio:       0.50,
			MinRecentMessages: 2,
			MaxSummaryChars:   3000,
			MaxFacts:          100,
		},
	}

	memory.SetMessages(schema.UserMessage("The failing request is /api/v1/card/create and has request_id=req-42."))
	memory.SetMessages(schema.AssistantMessage("The request returned HTTP 500 with evidence_id=ev_profile_123 and error_code=ERR_PROFILE_DOWN.", nil))
	for index := 0; index < 20; index++ {
		memory.SetMessages(schema.UserMessage("unrelated diagnostic noise " + strings.Repeat("noise ", 20)))
		memory.SetMessages(schema.AssistantMessage("unrelated result "+strings.Repeat("result ", 20), nil))
	}

	if memory.Compaction.Generation == 0 {
		t.Fatal("expected crowded history to trigger compaction")
	}
	if len(memory.GetMessages()) >= 40 {
		t.Fatalf("expected old messages to move out of active history, got %d messages", len(memory.GetMessages()))
	}

	contextSummary := memory.GetContextSummary(6000)
	for _, detail := range []string{
		"/api/v1/card/create",
		"HTTP 500",
		"req-42",
		"ev_profile_123",
		"ERR_PROFILE_DOWN",
	} {
		if !strings.Contains(contextSummary, detail) {
			t.Fatalf("expected exact detail %q after compaction, summary=%q", detail, contextSummary)
		}
	}
}

func TestCompactionKeepsRecentMessagesAndTracksTokenCounts(t *testing.T) {
	memory := &SimpleMemory{
		ID:            "token-threshold",
		MaxWindowSize: 20,
		Policy: CompactionPolicy{
			MaxTokens:         100,
			TriggerRatio:      0.70,
			TargetRatio:       0.50,
			MinRecentMessages: 2,
			MaxSummaryChars:   2000,
			MaxFacts:          20,
		},
	}
	memory.SetMessages(schema.UserMessage("old question with evidence_id=ev_old_1"))
	memory.SetMessages(schema.AssistantMessage("old answer HTTP 503", nil))
	memory.SetMessages(schema.UserMessage("recent question"))
	memory.SetMessages(schema.AssistantMessage("recent answer", nil))
	memory.SetMessages(schema.UserMessage(strings.Repeat("current detail ", 30)))

	if memory.Compaction.Generation == 0 {
		t.Fatal("expected token threshold to trigger compaction")
	}
	if memory.Compaction.BeforeTokens <= 0 || memory.Compaction.AfterTokens <= 0 {
		t.Fatalf("expected token counts in compaction metadata, metadata=%#v", memory.Compaction)
	}
	messages := memory.GetMessages()
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "current detail") {
		t.Fatalf("expected newest message to remain active, messages=%#v", messages)
	}
	if !strings.Contains(memory.GetContextSummary(4000), "ev_old_1") {
		t.Fatal("expected old evidence id to remain in exact facts")
	}
}

func TestSQLiteContextSnapshotRoundTrip(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "telltale.db")
	t.Setenv("SUPERBIZ_SQLITE_PATH", databasePath)
	ctx := context.Background()
	if err := storage.Initialize(ctx); err != nil {
		t.Fatalf("initialize sqlite storage: %v", err)
	}

	conversationID := "sqlite-compaction-" + time.Now().Format("150405.000000000")
	memory := &SimpleMemory{
		ID:            conversationID,
		UserID:        "local-user",
		MaxWindowSize: 20,
		Policy: CompactionPolicy{
			MaxTokens:         80,
			TriggerRatio:      0.70,
			TargetRatio:       0.50,
			MinRecentMessages: 2,
			MaxSummaryChars:   2000,
			MaxFacts:          20,
		},
		store: newSQLiteMemoryStore(),
	}
	memory.SetMessages(schema.UserMessage("old request evidence_id=ev_sqlite_1 HTTP 500"))
	memory.SetMessages(schema.AssistantMessage(strings.Repeat("old answer ", 30), nil))
	memory.SetMessages(schema.UserMessage("new request"))
	memory.SetMessages(schema.AssistantMessage("new answer", nil))

	if memory.Compaction.Generation == 0 {
		t.Fatal("expected sqlite fixture to compact")
	}
	memory.AddContextFacts([]ContextFact{
		{Kind: "claim_id", Value: "claim_callback_root_cause", SourceRole: "claim_pipeline"},
		{Kind: "coverage_status", Value: "claim_callback_root_cause=unknown", SourceRole: "matcher"},
		{Kind: "failed_query_id", Value: "fq_dependency_check", SourceRole: "diagnostic_loop"},
		{Kind: "diagnostic_note", Value: "password=not-persisted", SourceRole: "tool_output"},
	})
	turnCount, err := g.DB().Model("conversation_turns").Where("conversation_id", conversationID).Count()
	if err != nil {
		t.Fatalf("count archived conversation turns: %v", err)
	}
	if turnCount != 4 {
		t.Fatalf("expected raw turns to remain archived after compaction, got %d", turnCount)
	}
	reloaded, err := newSQLiteMemoryStore().Load(conversationID)
	if err != nil {
		t.Fatalf("load sqlite context snapshot: %v", err)
	}
	if reloaded == nil || reloaded.Compaction.Generation == 0 {
		t.Fatalf("expected persisted compaction metadata, snapshot=%#v", reloaded)
	}
	if !strings.Contains(reloaded.Summary, "ev_sqlite_1") && !containsFact(reloaded.Facts, "ev_sqlite_1") {
		t.Fatalf("expected persisted evidence detail, summary=%q facts=%#v", reloaded.Summary, reloaded.Facts)
	}
	for _, value := range []string{"claim_callback_root_cause", "claim_callback_root_cause=unknown", "fq_dependency_check"} {
		if !containsFact(reloaded.Facts, value) {
			t.Fatalf("expected structured context fact %q after reload, facts=%#v", value, reloaded.Facts)
		}
	}
	if containsFact(reloaded.Facts, "password=not-persisted") {
		t.Fatalf("sensitive context fact must not survive SQLite persistence: %#v", reloaded.Facts)
	}
}

func TestDynamicCompactionMaxTokensIsBounded(t *testing.T) {
	limit := DynamicCompactionMaxTokens(context.Background())
	if limit < defaultDynamicCompactionMinTokens || limit > defaultDynamicCompactionMaxTokens {
		t.Fatalf("expected dynamic compaction limit in [%d,%d], got %d", defaultDynamicCompactionMinTokens, defaultDynamicCompactionMaxTokens, limit)
	}
}

func containsFact(facts []ContextFact, value string) bool {
	for _, fact := range facts {
		if fact.Value == value {
			return true
		}
	}
	return false
}
