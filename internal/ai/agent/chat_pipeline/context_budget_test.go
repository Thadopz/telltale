package chat_pipeline

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

func TestAgentPromptOverheadGrowsWithRegisteredTools(t *testing.T) {
	one := estimateAgentPromptOverhead(context.Background(), coordinatorDefinition, []tool.BaseTool{namedTestTool{name: "tool_1"}})
	manyTools := make([]namedTestTool, 100)
	for index := range manyTools {
		manyTools[index] = namedTestTool{name: "tool_" + string(rune('a'+index%26))}
	}
	toolList := make([]tool.BaseTool, 0, len(manyTools))
	for _, candidate := range manyTools {
		toolList = append(toolList, candidate)
	}
	if got := estimateAgentPromptOverhead(context.Background(), coordinatorDefinition, toolList); got <= one {
		t.Fatalf("expected 100 tool schemas to increase prompt overhead, one=%d many=%d", one, got)
	}
}

func TestBudgetModelContextUsesProfileAndPreservesCurrentRequest(t *testing.T) {
	plan := contextBudgetPlan{
		Profile: contextbudget.ModelContextProfile{
			ConfigKey:             "test_model",
			ModelName:             "test-model",
			ContextWindowTokens:   32768,
			MaxOutputTokens:       4096,
			WorkingPromptCap:      16384,
			SafetyMarginRatio:     0.10,
			MinSafetyMarginTokens: 2048,
		},
		PromptOverhead: 1000,
	}
	modelContext := ModelContext{
		Content:         "find the root cause for request req-42",
		RequestAnalysis: "diagnostic request analysis",
		Summary:         strings.Repeat("old summary noise ", 1000) + " evidence_id=ev_critical_1",
		SessionState:    strings.Repeat("old state ", 1000),
		DurableMemory:   strings.Repeat("old memory ", 1000),
		ToolResults:     strings.Repeat("old tool result ", 1000),
		ProjectContext:  strings.Repeat("old project rule ", 1000),
		Skills:          strings.Repeat("old skill text ", 1000),
	}

	got, err := budgetModelContextWithPlan(context.Background(), modelContext, plan)
	if err != nil {
		t.Fatalf("budget context: %v", err)
	}
	if got.Content != modelContext.Content || got.RequestAnalysis != modelContext.RequestAnalysis {
		t.Fatalf("current request fields were changed: %#v", got)
	}
	if !strings.Contains(got.Summary, "ev_critical_1") {
		t.Fatalf("expected exact summary detail to survive trimming, got %q", got.Summary)
	}
	if estimateModelContextTokens(got) > got.Budget.FinalBudgetTokens {
		t.Fatalf("context exceeds final budget: estimated=%d budget=%d", estimateModelContextTokens(got), got.Budget.FinalBudgetTokens)
	}
	if len(got.Budget.TrimmedLayers) == 0 {
		t.Fatalf("expected crowded context to record trimmed layers")
	}
}

func TestBudgetModelContextRejectsOversizedCurrentRequest(t *testing.T) {
	plan := contextBudgetPlan{
		Profile: contextbudget.ModelContextProfile{
			ModelName:             "small-model",
			ContextWindowTokens:   8192,
			MaxOutputTokens:       4096,
			WorkingPromptCap:      8192,
			SafetyMarginRatio:     0.10,
			MinSafetyMarginTokens: 2048,
		},
	}
	_, err := budgetModelContextWithPlan(context.Background(), ModelContext{
		Content: strings.Repeat("current request ", 10000),
	}, plan)
	if err == nil || !strings.Contains(err.Error(), "context_budget_exceeded") {
		t.Fatalf("expected explicit context budget error, got %v", err)
	}
}

func TestModelTraceReceivesBudgetAndTrimmedLayers(t *testing.T) {
	chatModel := &testADKModel{}
	profile := &contextbudget.ModelContextProfile{
		ConfigKey:             "test_model",
		ModelName:             "test-model",
		ContextWindowTokens:   8192,
		MaxOutputTokens:       2048,
		WorkingPromptCap:      8192,
		SafetyMarginRatio:     0.10,
		MinSafetyMarginTokens: 2048,
	}
	runner, err := BuildChatAgentWithOptions(context.Background(), ChatAgentOptions{
		ChatModel:      chatModel,
		Tools:          []tool.BaseTool{},
		MaxIterations:  2,
		ContextProfile: profile,
	})
	if err != nil {
		t.Fatalf("build chat pipeline: %v", err)
	}
	recorder := trace.NewRecorder()
	_, err = runner.Invoke(context.Background(), &UserMessage{
		Query:   "find the root cause",
		Summary: strings.Repeat("historical summary noise ", 2000) + " evidence_id=ev_trace_budget",
	}, compose.WithCallbacks(trace.Callback("budget_test", recorder)))
	if err != nil {
		t.Fatalf("invoke chat pipeline: %v", err)
	}

	for _, step := range recorder.Steps() {
		if step.Component != string(components.ComponentOfChatModel) || step.Phase != "start" {
			continue
		}
		if step.ContextWindowTokens != profile.ContextWindowTokens || step.InputBudgetTokens <= 0 {
			t.Fatalf("expected model window and input budget in trace, got %#v", step)
		}
		if len(step.TrimmedLayers) == 0 {
			t.Fatalf("expected trimmed layers in model trace, got %#v", step)
		}
		return
	}
	t.Fatal("expected a chat-model start step")
}
