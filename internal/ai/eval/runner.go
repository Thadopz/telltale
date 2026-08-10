package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
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

	variantCount := 1
	if opts.Mode == ModeContextStress {
		variantCount = len(contextStressVariants())
	}
	results := make([]EvalCaseResult, 0, len(cases)*opts.Repetitions*variantCount)
	for _, evalCase := range cases {
		if err := validateCaseForMode(evalCase, opts.Mode); err != nil {
			return nil, err
		}
		for attempt := 1; attempt <= opts.Repetitions; attempt++ {
			variants := []string{""}
			if opts.Mode == ModeContextStress {
				variants = contextStressVariants()
			}
			for _, variant := range variants {
				caseCtx := ctx
				cancel := func() {}
				if opts.CaseTimeoutSeconds > 0 {
					caseCtx, cancel = context.WithTimeout(ctx, time.Duration(opts.CaseTimeoutSeconds)*time.Second)
				}
				variantOptions := opts
				variantOptions.ContextStressVariant = variant
				result, err := RunCase(caseCtx, evalCase, variantOptions, attempt)
				cancel()
				if err != nil {
					return nil, err
				}
				results = append(results, result)
			}
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
	case ModeContextStress:
		if evalCase.ContextStress == nil {
			return fmt.Errorf("context stress case %q requires context_stress settings", evalCase.ID)
		}
		if len(evalCase.ModelSteps) > 0 || len(evalCase.FailoverModelSteps) > 0 {
			return fmt.Errorf("context stress case %q must not contain scripted model steps", evalCase.ID)
		}
	case ModeQueryResultPressure:
		if evalCase.QueryResultPressure == nil {
			return fmt.Errorf("query result pressure case %q requires query_result_pressure settings", evalCase.ID)
		}
		if len(evalCase.ModelSteps) > 0 || len(evalCase.FailoverModelSteps) > 0 {
			return fmt.Errorf("query result pressure case %q must not contain scripted model steps", evalCase.ID)
		}
	default:
		return fmt.Errorf("unsupported eval mode %q", mode)
	}
	return nil
}

func RunCase(ctx context.Context, evalCase Case, opts RunOptions, attempt int) (EvalCaseResult, error) {
	startedAt := time.Now()
	conversationID := fmt.Sprintf("eval-%s-%s-%d", sanitizeID(evalCase.ID), opts.RunID, attempt)
	if opts.ContextStressVariant != "" {
		conversationID += "-" + sanitizeID(opts.ContextStressVariant)
	}
	if opts.Mode == ModeSmoke || opts.Mode == ModeQueryResultPressure {
		ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: "eval", Roles: []string{"oncall"}})
	}

	preparedCase := evalCase
	if opts.Mode == ModeContextStress {
		var err error
		preparedCase, err = prepareContextStressCase(evalCase, opts.ContextStressVariant)
		if err != nil {
			return EvalCaseResult{}, err
		}
	} else if opts.Mode == ModeQueryResultPressure {
		var err error
		preparedCase, err = prepareQueryResultPressureCase(evalCase)
		if err != nil {
			return EvalCaseResult{}, err
		}
	}

	userMessage := buildUserMessage(preparedCase, conversationID)
	result := EvalCaseResult{
		ID:             evalCase.ID,
		Attempt:        attempt,
		Variant:        opts.ContextStressVariant,
		Category:       evalCase.Category,
		Mode:           opts.Mode,
		Query:          evalCase.Query,
		ConversationID: conversationID,
		StartedAt:      startedAt,
		Context: ContextEvidence{
			SummaryChars:          utf8.RuneCountInString(preparedCase.Context.Summary),
			SessionStateChars:     utf8.RuneCountInString(preparedCase.Context.SessionState),
			DurableMemoryChars:    utf8.RuneCountInString(preparedCase.Context.DurableMemory),
			ToolResultsChars:      utf8.RuneCountInString(preparedCase.Context.ToolResults),
			HistoryMessages:       len(preparedCase.History),
			PromptContextChars:    casePromptContextChars(preparedCase),
			CurrentRequestPresent: strings.TrimSpace(preparedCase.Query) != "",
		},
	}
	if opts.Mode == ModeContextStress {
		spec := normalizeContextStressSpec(preparedCase.ContextStress)
		result.Context.Variant = opts.ContextStressVariant
		result.Context.RegisteredToolCount = len(preparedCase.ToolFixtures)
		result.Context.CriticalFacts = append([]string(nil), spec.CriticalFacts...)
		result.Context.CriticalFactsSeen = factsInText(caseContextText(preparedCase), spec.CriticalFacts)
		result.Context.ModelVisibleToolMarkerHits = contextStressToolMarkerHits(preparedCase.ToolFixtures)
	}
	if opts.Mode == ModeQueryResultPressure {
		result.Context.RegisteredToolCount = len(preparedCase.ToolFixtures)
		result.Context.QuerySourceRows = normalizeQueryResultPressureSpec(preparedCase.QueryResultPressure).SourceRows
	}

	var stressModel *contextStressModel
	if opts.Mode == ModeContextStress && !opts.RealModel {
		spec := normalizeContextStressSpec(preparedCase.ContextStress)
		stressModel = newContextStressModel(spec.CriticalFacts, preparedCase.Query)
	}
	var pressureModel *queryResultPressureModel
	if opts.Mode == ModeQueryResultPressure && !opts.RealModel {
		pressureModel = newQueryResultPressureModel()
	}
	agentOptions := optionsForCase(preparedCase, opts, stressModel, pressureModel)
	recorder := trace.NewRecorder()
	runner, err := chat_pipeline.BuildChatAgentWithOptions(ctx, agentOptions)
	if err != nil {
		result.Error = err.Error()
		finishResult(&result, recorder, startedAt)
		recordContextStressDiagnostics(&result, preparedCase, stressModel)
		recordQueryResultPressureDiagnostics(&result, preparedCase, pressureModel)
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
	result.DraftAnswer = result.Answer
	finishResult(&result, recorder, startedAt)
	if shouldRunStructuredClaimPipeline(preparedCase, opts) {
		pipelineResult := &chat_pipeline.ClaimPipelineResult{}
		claimExtractor := structuredClaimExtractorForCase(preparedCase, opts)
		finalAnswer, convergeErr := chat_pipeline.ConvergeAnswer(ctx, chat_pipeline.AnswerConvergeInput{
			Question:       preparedCase.Query,
			Answer:         result.DraftAnswer,
			Steps:          result.TraceSteps,
			ToolEvidence:   recorder.ToolEvidence(),
			ClaimExtractor: claimExtractor,
			PipelineResult: pipelineResult,
			Recorder:       recorder,
		})
		result.ClaimExtractionError = pipelineResult.ExtractionError
		result.LocalizationMode = pipelineResult.LocalizationMode
		if pipelineResult.ClaimSet.SchemaVersion > 0 {
			claimSet := pipelineResult.ClaimSet
			result.ClaimSet = &claimSet
		}
		result.CoverageReports = append([]evidence.CoverageReport(nil), pipelineResult.CoverageReports...)
		if convergeErr != nil {
			result.Error = convergeErr.Error()
		} else {
			result.Answer = finalAnswer
		}
		finishResult(&result, recorder, startedAt)
	}
	recordContextStressDiagnostics(&result, preparedCase, stressModel)
	recordQueryResultPressureDiagnostics(&result, preparedCase, pressureModel)
	if opts.EnableJudge {
		result.Judge = JudgeCase(ctx, evalCase, result)
	}
	result.Scores = ScoreCase(evalCase, result)
	return result, nil
}

func shouldRunStructuredClaimPipeline(evalCase Case, opts RunOptions) bool {
	if evalCase.Claims != nil {
		return true
	}
	return opts.RealModel && opts.Mode == ModeContextStress
}

func structuredClaimExtractorForCase(evalCase Case, opts RunOptions) chat_pipeline.ClaimExtractor {
	if opts.RealModel || evalCase.Claims == nil {
		return nil
	}
	return fixtureClaimExtractor{claimSet: *evalCase.Claims}
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

func optionsForCase(evalCase Case, opts RunOptions, stressModel *contextStressModel, pressureModel *queryResultPressureModel) chat_pipeline.ChatAgentOptions {
	switch opts.Mode {
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
	case ModeContextStress:
		noRetries := 0
		spec := normalizeContextStressSpec(evalCase.ContextStress)
		profile := contextStressProfile(spec, opts.ContextStressVariant)
		options := chat_pipeline.ChatAgentOptions{
			Tools:                  fixtureTools(evalCase.ToolFixtures),
			MaxIterations:          6,
			ModelRetries:           &noRetries,
			ContextProfile:         &profile,
			FailoverContextProfile: &profile,
			DisableToolSearch:      true,
		}
		if !opts.RealModel {
			options.ChatModel = stressModel
		}
		return options
	case ModeQueryResultPressure:
		noRetries := 0
		options := chat_pipeline.ChatAgentOptions{
			Tools:             fixtureTools(evalCase.ToolFixtures),
			MaxIterations:     4,
			ModelRetries:      &noRetries,
			DisableToolSearch: true,
			AgentScope:        authz.AgentScopeDatabase,
		}
		if !opts.RealModel {
			options.ChatModel = pressureModel
		}
		return options
	default:
		return chat_pipeline.ChatAgentOptions{}
	}
}

func finishResult(result *EvalCaseResult, recorder *trace.Recorder, startedAt time.Time) {
	result.ElapsedMS = time.Since(startedAt).Milliseconds()
	result.TraceSteps = recorder.Steps()
	result.ToolCalls = toolEvidenceFromRecorder(recorder.ToolEvidence())
	result.Context.UseKnowledge = usedKnowledgeTool(result.ToolCalls)
	result.Metrics = executionMetrics(result.TraceSteps, result.ToolCalls)
	copyBudgetEvidence(result)
}

func toolEvidenceFromRecorder(items []trace.ToolEvidence) []ToolEvidence {
	result := make([]ToolEvidence, 0, len(items))
	for _, item := range items {
		result = append(result, ToolEvidence{
			Name:       item.ToolName,
			Input:      item.InputJSON,
			Output:     item.OutputJSON,
			Status:     item.Status,
			Error:      item.Envelope.Error,
			EvidenceID: item.EvidenceID,
		})
	}
	return result
}

func copyBudgetEvidence(result *EvalCaseResult) {
	for _, step := range result.TraceSteps {
		if step.Component != string(components.ComponentOfChatModel) || step.Phase != "start" {
			continue
		}
		result.Context.EstimatedPromptTokens = step.EstimatedPromptTokens
		result.Context.FinalBudgetTokens = step.FinalBudgetTokens
		result.Context.InputLimitTokens = step.InputBudgetTokens
		result.Context.PromptOverheadTokens = step.PromptOverheadTokens
		result.Context.SafetyMarginTokens = step.SafetyMarginTokens
		result.Context.ContextWindowTokens = step.ContextWindowTokens
		result.Context.CalibrationFactor = step.CalibrationFactor
		result.Context.TrimmedLayers = append([]string(nil), step.TrimmedLayers...)
		return
	}
}

func recordContextStressDiagnostics(result *EvalCaseResult, evalCase Case, stressModel *contextStressModel) {
	if result == nil || evalCase.ContextStress == nil {
		return
	}
	spec := normalizeContextStressSpec(evalCase.ContextStress)
	if stressModel != nil {
		if names := stressModel.ToolNames(); len(names) > 0 {
			result.Context.RegisteredToolCount = len(names)
		}
		result.Context.CriticalFactsSeen = stressModel.SeenFacts()
		result.Context.MissingCriticalFacts = stressModel.MissingFacts()
		result.Context.CurrentRequestPresent = stressModel.QuerySeen()
	}
	distractorNames := contextStressDistractorNames(resultToolFixtures(evalCase, spec))
	for _, call := range result.ToolCalls {
		if distractorNames[call.Name] {
			result.Context.DistractorToolCalls = appendUniqueStrings(result.Context.DistractorToolCalls, call.Name)
		}
	}
	if len(result.Context.MissingCriticalFacts) == 0 {
		for _, fact := range spec.CriticalFacts {
			found := false
			for _, seen := range result.Context.CriticalFactsSeen {
				if seen == fact {
					found = true
					break
				}
			}
			if !found {
				result.Context.MissingCriticalFacts = append(result.Context.MissingCriticalFacts, fact)
			}
		}
	}
}

func resultToolFixtures(evalCase Case, spec ContextStressSpec) []ToolFixture {
	if len(evalCase.ToolFixtures) > 0 {
		return evalCase.ToolFixtures
	}
	return contextStressToolFixtures(spec.DistractorToolCount)
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

func casePromptContextChars(evalCase Case) int {
	return utf8.RuneCountInString(caseContextText(evalCase))
}

func caseContextText(evalCase Case) string {
	var builder strings.Builder
	builder.WriteString(evalCase.Query)
	builder.WriteString("\n")
	builder.WriteString(evalCase.Context.Summary)
	builder.WriteString("\n")
	builder.WriteString(evalCase.Context.SessionState)
	builder.WriteString("\n")
	builder.WriteString(evalCase.Context.DurableMemory)
	builder.WriteString("\n")
	builder.WriteString(evalCase.Context.ToolResults)
	for _, message := range evalCase.History {
		builder.WriteString("\n")
		builder.WriteString(message.Content)
	}
	return builder.String()
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
