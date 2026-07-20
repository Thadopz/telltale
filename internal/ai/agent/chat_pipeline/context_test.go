package chat_pipeline

import (
	"SREagent/internal/authz"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

func TestAssembleModelContextBuildsPromptValues(t *testing.T) {
	history := []*schema.Message{schema.UserMessage("old question")}
	input := &UserMessage{
		Query:         "  show the runbook  ",
		History:       history,
		Summary:       "  earlier summary  ",
		SessionState:  "  active incident  ",
		DurableMemory: "  prefer concise answers  ",
		ToolResults:   "  log query returned 3 rows  ",
	}

	got := AssembleModelContext(context.Background(), input, time.Date(2026, time.May, 21, 9, 30, 0, 0, time.UTC))
	if got.Content != "show the runbook" {
		t.Fatalf("expected trimmed content, got %q", got.Content)
	}
	if got.Summary != "earlier summary" || got.SessionState != "active incident" {
		t.Fatalf("expected trimmed context, got summary=%q state=%q", got.Summary, got.SessionState)
	}
	if got.DurableMemory != "prefer concise answers" || got.ToolResults != "log query returned 3 rows" {
		t.Fatalf("expected trimmed memory and tools, got memory=%q tools=%q", got.DurableMemory, got.ToolResults)
	}
	if got.Date != "2026-05-21 09:30:00" {
		t.Fatalf("expected formatted date, got %q", got.Date)
	}
	if strings.Contains(got.RequestAnalysis, "Need Knowledge") || strings.Contains(got.RequestAnalysis, "Knowledge Query") {
		t.Fatalf("request analysis must not make a static retrieval decision: %q", got.RequestAnalysis)
	}

	got.ProjectContext = "project rules"
	values := got.PromptValues()
	if values["project_context"] != "project rules" || values["request_analysis"] != got.RequestAnalysis {
		t.Fatalf("unexpected prompt values: %#v", values)
	}

	got.History[0].Content = "changed"
	if history[0].Content != "old question" {
		t.Fatalf("expected history clone, got %q", history[0].Content)
	}
}

func TestAssembleModelContextAppliesContextBudget(t *testing.T) {
	input := &UserMessage{
		Query:         "hello",
		Summary:       strings.Repeat("s", SummaryMaxChars+20),
		ToolResults:   strings.Repeat("t", ToolResultsMaxChars+20),
		DurableMemory: strings.Repeat("d", DurableMemoryMaxChars+20),
	}

	got := AssembleModelContext(context.Background(), input, time.Now())
	if utf8.RuneCountInString(got.Summary) != SummaryMaxChars {
		t.Fatalf("expected summary budget %d", SummaryMaxChars)
	}
	if utf8.RuneCountInString(got.ToolResults) != ToolResultsMaxChars {
		t.Fatalf("expected tool result budget %d", ToolResultsMaxChars)
	}
	if utf8.RuneCountInString(got.DurableMemory) != DurableMemoryMaxChars {
		t.Fatalf("expected durable memory budget %d", DurableMemoryMaxChars)
	}
}

func TestBudgetHistoryMessagesKeepsRecentUserAssistantPair(t *testing.T) {
	history := []*schema.Message{
		schema.UserMessage("old question"),
		schema.AssistantMessage("old answer", nil),
		schema.UserMessage("recent question"),
		schema.AssistantMessage("recent answer", nil),
	}

	got := budgetHistoryMessages(history, len("recent question")+len("recent answer"))
	if len(got) != 2 || got[0].Content != "recent question" || got[1].Content != "recent answer" {
		t.Fatalf("expected recent pair, got %#v", got)
	}
}

func TestPreparedModelContextIsReused(t *testing.T) {
	modelContext := ModelContext{Content: "cached content", RequestAnalysis: "cached analysis", Date: "2026-05-21 09:30:00"}
	got, err := newInputToChatLambda(context.Background(), &UserMessage{Query: "different content", ModelContext: &modelContext})
	if err != nil {
		t.Fatalf("input conversion: %v", err)
	}
	if got["content"] != "cached content" || got["date"] != "2026-05-21 09:30:00" {
		t.Fatalf("expected prepared context, got %#v", got)
	}
}

func TestInputToChatLoadsProjectContext(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	directory := t.TempDir()
	if err = os.WriteFile(filepath.Join(directory, "AGENT.md"), []byte("Use project evidence rules."), 0o644); err != nil {
		t.Fatalf("write AGENT.md: %v", err)
	}
	if err = os.Chdir(directory); err != nil {
		t.Fatalf("change directory: %v", err)
	}
	defer func() { _ = os.Chdir(workingDirectory) }()

	got, err := newInputToChatLambda(context.Background(), &UserMessage{Query: "hello"})
	if err != nil {
		t.Fatalf("input conversion: %v", err)
	}
	if got["project_context"] != "Use project evidence rules." {
		t.Fatalf("expected project context, got %#v", got["project_context"])
	}
}

func TestChatAgentFactoryCacheKeySeparatesRoles(t *testing.T) {
	factory := NewChatAgentFactory()
	viewerKey := factory.cacheKey(authz.WithPrincipal(context.Background(), authz.Principal{UserID: "u1", Roles: []string{"viewer"}}))
	oncallKey := factory.cacheKey(authz.WithPrincipal(context.Background(), authz.Principal{UserID: "u2", Roles: []string{"oncall"}}))
	if viewerKey == oncallKey {
		t.Fatalf("expected role-specific cache keys, got %q", viewerKey)
	}
}
