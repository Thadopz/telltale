package chat_pipeline

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/input_preprocessor"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

type ModelContext struct {
	Content         string
	History         []*schema.Message
	Summary         string
	SessionState    string
	DurableMemory   string
	ToolResults     string
	ProjectContext  string
	RequestAnalysis string
	Skills          string
	Date            string
	Budget          contextbudget.BudgetReport `json:"-"`
	BudgetError     string                     `json:"-"`
}

func AssembleModelContext(ctx context.Context, input *UserMessage, now time.Time) ModelContext {
	modelContext := assembleRawModelContext(ctx, input, now)
	return budgetModelContext(ctx, modelContext)
}

func assembleRawModelContext(ctx context.Context, input *UserMessage, now time.Time) ModelContext {
	if input == nil {
		return ModelContext{Date: now.Format("2006-01-02 15:04:05")}
	}

	processed := input_preprocessor.Process(ctx, input.Query, now)
	modelContext := ModelContext{
		Content:         processed.CleanQuery,
		History:         cloneHistory(input.History),
		Summary:         strings.TrimSpace(input.Summary),
		SessionState:    strings.TrimSpace(input.SessionState),
		DurableMemory:   strings.TrimSpace(input.DurableMemory),
		ToolResults:     strings.TrimSpace(input.ToolResults),
		RequestAnalysis: processed.FormatPrompt(),
		Date:            now.Format("2006-01-02 15:04:05"),
	}
	return modelContext
}

func (c ModelContext) PromptValues() map[string]any {
	values := map[string]any{
		"content":          c.Content,
		"history":          c.History,
		"summary":          c.Summary,
		"session_state":    c.SessionState,
		"durable_memory":   c.DurableMemory,
		"tool_results":     c.ToolResults,
		"project_context":  c.ProjectContext,
		"request_analysis": c.RequestAnalysis,
		"skills":           c.Skills,
		"date":             c.Date,
	}
	if c.Budget.FinalBudgetTokens > 0 || len(c.Budget.TrimmedLayers) > 0 {
		// This private value is consumed by budgetAwareChatTemplate and is
		// never rendered into the prompt. It lets model callbacks retain
		// aggregate trimming metadata without putting prompt contents in the
		// context or trace.
		values[budgetReportPromptValueKey] = c.Budget
	}
	return values
}

func cloneHistory(history []*schema.Message) []*schema.Message {
	cloned := make([]*schema.Message, 0, len(history))
	for _, msg := range history {
		if msg == nil {
			continue
		}
		msgCopy := *msg
		cloned = append(cloned, &msgCopy)
	}
	return cloned
}
