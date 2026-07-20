package chat_pipeline

import (
	"SREagent/internal/ai/input_preprocessor"
	"context"
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
}

func AssembleModelContext(ctx context.Context, input *UserMessage, now time.Time) ModelContext {
	if input == nil {
		return ModelContext{Date: now.Format("2006-01-02 15:04:05")}
	}

	processed := input_preprocessor.Process(ctx, input.Query, now)
	return ModelContext{
		Content:         processed.CleanQuery,
		History:         budgetHistoryMessages(input.History, HistoryMaxChars),
		Summary:         trimRunesHead(strings.TrimSpace(input.Summary), SummaryMaxChars),
		SessionState:    trimRunesHead(strings.TrimSpace(input.SessionState), SessionStateMaxChars),
		DurableMemory:   trimRunesHead(strings.TrimSpace(input.DurableMemory), DurableMemoryMaxChars),
		ToolResults:     trimRunesHead(strings.TrimSpace(input.ToolResults), ToolResultsMaxChars),
		RequestAnalysis: processed.FormatPrompt(),
		Date:            now.Format("2006-01-02 15:04:05"),
	}
}

func (c ModelContext) PromptValues() map[string]any {
	return map[string]any{
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
