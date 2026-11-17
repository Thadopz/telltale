package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestContextStressToolCatalogHasNoTestMarkers(t *testing.T) {
	fixtures := contextStressToolFixtures(100)
	if len(fixtures) != 101 {
		t.Fatalf("expected one target and 100 distractor tools, got %d", len(fixtures))
	}
	if hits := contextStressToolMarkerHits(fixtures); len(hits) != 0 {
		t.Fatalf("model-visible tool metadata contains test markers: %v", hits)
	}

	names := map[string]bool{}
	for _, fixture := range fixtures {
		if names[fixture.Name] {
			t.Fatalf("duplicate context stress tool name %q", fixture.Name)
		}
		names[fixture.Name] = true
		if len(fixture.Parameters) == 0 {
			t.Fatalf("tool %q has no realistic parameter schema", fixture.Name)
		}
	}
}

func TestContextStressModelRejectsMissingFactBeforeToolCall(t *testing.T) {
	model := newContextStressModel([]string{"ERR_PAYMENT_CALLBACK_TIMEOUT", "/api/v1/payment/callback"}, "payment callback")
	_, err := model.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("ERR_PAYMENT_CALLBACK_TIMEOUT payment callback"),
	})
	if err == nil || !strings.Contains(err.Error(), "context_stress_missing_fact") {
		t.Fatalf("expected missing fact error, got %v", err)
	}
	if len(model.SeenFacts()) != 0 {
		t.Fatalf("missing-fact call must not be recorded as complete: %v", model.SeenFacts())
	}
}

func TestContextStressModelChecksFactsAcrossToolLoop(t *testing.T) {
	facts := []string{"ERR_PAYMENT_CALLBACK_TIMEOUT", "/api/v1/payment/callback"}
	model := newContextStressModel(facts, "payment callback")
	if _, err := model.WithTools([]*schema.ToolInfo{{Name: "observability_agent"}}); err != nil {
		t.Fatalf("attach target tool: %v", err)
	}
	messages := []*schema.Message{
		schema.UserMessage("payment callback ERR_PAYMENT_CALLBACK_TIMEOUT /api/v1/payment/callback"),
	}

	first, err := model.Generate(context.Background(), messages)
	if err != nil {
		t.Fatalf("first model call: %v", err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Function.Name != "observability_agent" {
		t.Fatalf("expected observability_agent tool call, got %#v", first.ToolCalls)
	}

	secondMessages := append(messages, &schema.Message{
		Role:     schema.Tool,
		ToolName: "observability_agent",
		Content:  "payment evidence returned",
	})
	second, err := model.Generate(context.Background(), secondMessages)
	if err != nil {
		t.Fatalf("second model call: %v", err)
	}
	if !strings.Contains(second.Content, "callback worker disabled") {
		// The deterministic answer deliberately names the expected root cause.
		t.Fatalf("expected root cause in answer, got %q", second.Content)
	}
	if len(model.SeenFacts()) != len(facts) || !model.QuerySeen() {
		t.Fatalf("model did not retain facts and request: facts=%v query_seen=%v", model.SeenFacts(), model.QuerySeen())
	}
}

func TestPrepareContextStressCaseAddsFactsAndTools(t *testing.T) {
	evalCase := Case{
		ID:    "context-stress-fixture",
		Query: "inspect payment callback",
		ContextStress: &ContextStressSpec{
			CriticalFacts:        []string{"fact-one", "fact-two"},
			HistoryNoisePairs:    2,
			SummaryNoiseChars:    100,
			ToolResultNoiseChars: 100,
			DistractorToolCount:  100,
		},
	}

	prepared, err := prepareContextStressCase(evalCase, contextStressStressVariant)
	if err != nil {
		t.Fatalf("prepare context stress case: %v", err)
	}
	if len(prepared.ToolFixtures) != 101 {
		t.Fatalf("expected 101 tools, got %d", len(prepared.ToolFixtures))
	}
	if !strings.Contains(prepared.Context.Summary, "fact-one") || !strings.Contains(prepared.Context.ToolResults, "fact-two") {
		t.Fatalf("critical facts were not placed in generated context")
	}
}
