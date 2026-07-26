package eval

import (
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/authz"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func RunCases(ctx context.Context, cases []Case, opts RunOptions) ([]EvalCaseResult, error) {
	if opts.Mode == "" {
		opts.Mode = ModeContract
	}
	if opts.RunID == "" {
		opts.RunID = time.Now().Format("20060102-150405")
	}
	if opts.Repetitions <= 0 {
		opts.Repetitions = 1
	}

	results := make([]EvalCaseResult, 0, len(cases)*opts.Repetitions)
	for _, evalCase := range cases {
		if err := validateCaseForMode(evalCase, opts.Mode); err != nil {
			return nil, err
		}
		for attempt := 1; attempt <= opts.Repetitions; attempt++ {
			caseCtx := ctx
			cancel := func() {}
			if opts.CaseTimeoutSeconds > 0 {
				caseCtx, cancel = context.WithTimeout(ctx, time.Duration(opts.CaseTimeoutSeconds)*time.Second)
			}
			result, err := RunCase(caseCtx, evalCase, opts, attempt)
			cancel()
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func validateCaseForMode(evalCase Case, mode Mode) error {
	switch mode {
	case ModeContract:
		if len(evalCase.ModelSteps) == 0 {
			return fmt.Errorf("contract case %q requires model_steps", evalCase.ID)
		}
	case ModeRouting:
		if len(evalCase.ModelSteps) > 0 || len(evalCase.FailoverModelSteps) > 0 {
			return fmt.Errorf("routing case %q must not contain scripted model steps", evalCase.ID)
		}
	case ModeSmoke:
		if len(evalCase.ModelSteps) > 0 || len(evalCase.FailoverModelSteps) > 0 || len(evalCase.ToolFixtures) > 0 {
			return fmt.Errorf("smoke case %q must not contain scripted model steps or tool_fixtures", evalCase.ID)
		}
	default:
		return fmt.Errorf("unsupported eval mode %q", mode)
	}
	return nil
}

func RunCase(ctx context.Context, evalCase Case, opts RunOptions, attempt int) (EvalCaseResult, error) {
	startedAt := time.Now()
	conversationID := fmt.Sprintf("eval-%s-%s-%d", sanitizeID(evalCase.ID), opts.RunID, attempt)
	if opts.Mode == ModeSmoke {
		ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: "eval", Roles: []string{"oncall"}})
	}

	userMessage := buildUserMessage(evalCase, conversationID)
	modelContext := chat_pipeline.AssembleModelContext(ctx, userMessage, startedAt)
	result := EvalCaseResult{
		ID:             evalCase.ID,
		Attempt:        attempt,
		Category:       evalCase.Category,
		Mode:           opts.Mode,
		Query:          evalCase.Query,
		ConversationID: conversationID,
		StartedAt:      startedAt,
		Context: ContextEvidence{
			SummaryChars:       utf8.RuneCountInString(modelContext.Summary),
			SessionStateChars:  utf8.RuneCountInString(modelContext.SessionState),
			DurableMemoryChars: utf8.RuneCountInString(modelContext.DurableMemory),
			ToolResultsChars:   utf8.RuneCountInString(modelContext.ToolResults),
			HistoryMessages:    len(modelContext.History),
			PromptContextChars: promptContextChars(modelContext),
		},
	}

	agentOptions := optionsForCase(evalCase, opts.Mode)
	recorder := trace.NewRecorder()
	runner, err := chat_pipeline.BuildChatAgentWithOptions(ctx, agentOptions)
	if err != nil {
		result.Error = err.Error()
		finishResult(&result, recorder, startedAt)
		result.Scores = ScoreCase(evalCase, result)
		return result, nil
	}

	output, err := runner.Invoke(ctx, userMessage, compose.WithCallbacks(trace.Callback("eval_chat_agent", recorder)))
	if err != nil {
		result.Error = err.Error()
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
		}
	} else if output != nil {
		result.Answer = output.Content
	}
	finishResult(&result, recorder, startedAt)
	if opts.EnableJudge {
		result.Judge = JudgeCase(ctx, evalCase, result)
	}
	result.Scores = ScoreCase(evalCase, result)
	return result, nil
}

func buildUserMessage(evalCase Case, conversationID string) *chat_pipeline.UserMessage {
	return &chat_pipeline.UserMessage{
		ID:             conversationID,
		UserID:         "eval",
		ConversationID: conversationID,
		Query:          evalCase.Query,
		History:        schemaHistory(evalCase.History),
		Summary:        evalCase.Context.Summary,
		SessionState:   evalCase.Context.SessionState,
		DurableMemory:  evalCase.Context.DurableMemory,
		ToolResults:    evalCase.Context.ToolResults,
	}
}

func optionsForCase(evalCase Case, mode Mode) chat_pipeline.ChatAgentOptions {
	switch mode {
	case ModeContract:
		noRetries := 0
		var failoverModel *scriptedModel
		if len(evalCase.FailoverModelSteps) > 0 {
			failoverModel = newScriptedModel(evalCase.FailoverModelSteps)
		}
		return chat_pipeline.ChatAgentOptions{
			ChatModel:     newScriptedModel(evalCase.ModelSteps),
			FailoverModel: failoverModel,
			Tools:         fixtureTools(evalCase.ToolFixtures),
			MaxIterations: len(evalCase.ModelSteps) + len(evalCase.FailoverModelSteps) + 2,
			ModelRetries:  &noRetries,
		}
	case ModeRouting:
		return chat_pipeline.ChatAgentOptions{
			Tools:         fixtureTools(evalCase.ToolFixtures),
			MaxIterations: 8,
		}
	default:
		return chat_pipeline.ChatAgentOptions{}
	}
}

func finishResult(result *EvalCaseResult, recorder *trace.Recorder, startedAt time.Time) {
	result.ElapsedMS = time.Since(startedAt).Milliseconds()
	result.TraceSteps = recorder.Steps()
	result.ToolCalls = toolEvidenceFromTrace(result.TraceSteps)
	result.Context.UseKnowledge = usedKnowledgeTool(result.ToolCalls)
	result.Metrics = executionMetrics(result.TraceSteps, result.ToolCalls)
}

func usedKnowledgeTool(calls []ToolEvidence) bool {
	for _, call := range calls {
		if call.Name == "knowledge_agent" || call.Name == "query_internal_docs" {
			return true
		}
	}
	return false
}

func executionMetrics(steps []trace.AgentStep, calls []ToolEvidence) ExecutionMetrics {
	metrics := ExecutionMetrics{ToolCalls: len(calls)}
	for _, step := range steps {
		if step.Phase == "start" && step.Component == string(components.ComponentOfChatModel) {
			metrics.ModelCalls++
		}
		if step.Phase == "error" && step.Component == string(components.ComponentOfChatModel) {
			metrics.ModelErrors++
		}
		if step.Phase == "end" && step.Component == string(components.ComponentOfChatModel) {
			metrics.PromptTokens += step.PromptTokens
			metrics.CompletionTokens += step.CompletionTokens
			metrics.TotalTokens += step.TotalTokens
		}
	}
	for _, call := range calls {
		if isAgentName(call.Name) {
			metrics.AgentCalls++
		}
	}
	return metrics
}

func schemaHistory(history []ChatMessage) []*schema.Message {
	messages := make([]*schema.Message, 0, len(history))
	for _, message := range history {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "assistant":
			messages = append(messages, schema.AssistantMessage(message.Content, nil))
		case "system":
			messages = append(messages, schema.SystemMessage(message.Content))
		default:
			messages = append(messages, schema.UserMessage(message.Content))
		}
	}
	return messages
}

func promptContextChars(modelContext chat_pipeline.ModelContext) int {
	total := utf8.RuneCountInString(modelContext.Content) +
		utf8.RuneCountInString(modelContext.Summary) +
		utf8.RuneCountInString(modelContext.SessionState) +
		utf8.RuneCountInString(modelContext.DurableMemory) +
		utf8.RuneCountInString(modelContext.ToolResults) +
		utf8.RuneCountInString(modelContext.RequestAnalysis)
	for _, message := range modelContext.History {
		if message != nil {
			total += utf8.RuneCountInString(message.Content)
		}
	}
	return total
}

func toolEvidenceFromTrace(steps []trace.AgentStep) []ToolEvidence {
	starts := map[string][]trace.AgentStep{}
	var tools []ToolEvidence
	for _, step := range steps {
		if step.Component != string(components.ComponentOfTool) {
			continue
		}
		switch step.Phase {
		case "start":
			starts[step.Name] = append(starts[step.Name], step)
		case "end":
			start := popToolStart(starts, step.Name)
			status, outputError, evidenceID := toolOutputStatus(step.Output)
			tools = append(tools, ToolEvidence{
				Name:       step.Name,
				Input:      start.Input,
				Output:     step.Output,
				Status:     status,
				Error:      outputError,
				EvidenceID: evidenceID,
				Elapsed:    step.ElapsedMS,
			})
		case "error":
			start := popToolStart(starts, step.Name)
			tools = append(tools, ToolEvidence{
				Name:   step.Name,
				Input:  start.Input,
				Status: "error",
				Error:  step.Error,
			})
		}
	}
	return tools
}

func popToolStart(starts map[string][]trace.AgentStep, name string) trace.AgentStep {
	values := starts[name]
	if len(values) == 0 {
		return trace.AgentStep{}
	}
	start := values[0]
	starts[name] = values[1:]
	return start
}

func toolOutputStatus(output string) (status string, outputError string, evidenceID string) {
	normalizedOutput := strings.TrimSpace(output)
	var decodedOutput string
	if json.Unmarshal([]byte(normalizedOutput), &decodedOutput) == nil {
		normalizedOutput = decodedOutput
	}

	var envelope struct {
		Success    *bool  `json:"success"`
		Status     string `json:"status"`
		Error      string `json:"error"`
		EvidenceID string `json:"evidence_id"`
	}
	if json.Unmarshal([]byte(normalizedOutput), &envelope) != nil {
		return "success", "", ""
	}
	if (envelope.Success != nil && !*envelope.Success) || strings.EqualFold(envelope.Status, "error") {
		return "error", envelope.Error, envelope.EvidenceID
	}
	return "success", "", envelope.EvidenceID
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "case"
	}
	return strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z':
			return character
		case character >= 'A' && character <= 'Z':
			return character
		case character >= '0' && character <= '9':
			return character
		case character == '-' || character == '_':
			return character
		default:
			return '-'
		}
	}, value)
}
