package eval

import (
	"context"
	"encoding/json"
	"fmt"
	chatanswer "github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline/answer"
	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline/execution"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
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
	} else if opts.Mode == ModeEvidenceAblation {
		variantCount = len(EvidenceModes())
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
			} else if opts.Mode == ModeEvidenceAblation {
				variants = evidenceModeOrder(attempt)
			}
			for _, variant := range variants {
				caseCtx := ctx
				cancel := func() {}
				if opts.CaseTimeoutSeconds > 0 {
					caseCtx, cancel = context.WithTimeout(ctx, time.Duration(opts.CaseTimeoutSeconds)*time.Second)
				}
				variantOptions := opts
				if opts.Mode == ModeEvidenceAblation {
					variantOptions.EvidenceMode = EvidenceMode(variant)
				} else {
					variantOptions.ContextStressVariant = variant
				}
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

// evidenceModeOrder is a three-period Latin-square schedule. Each arm appears
// once in every position across three consecutive attempts.
func evidenceModeOrder(attempt int) []string {
	modes := EvidenceModes()
	start := (attempt - 1) % len(modes)
	ordered := make([]string, 0, len(modes))
	for offset := 0; offset < len(modes); offset++ {
		ordered = append(ordered, string(modes[(start+offset)%len(modes)]))
	}
	return ordered
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
	case ModeEvidenceAblation:
		if evalCase.Truth == nil {
			return fmt.Errorf("evidence ablation case %q requires atomic truth", evalCase.ID)
		}
		if err := validateAblationTruth(evalCase.ID, *evalCase.Truth); err != nil {
			return err
		}
		if len(evalCase.ToolFixtures) == 0 {
			return fmt.Errorf("evidence ablation case %q requires canonical tool fixtures", evalCase.ID)
		}
		if evalCase.AblationNoiseChars < 0 || evalCase.AblationNoiseChars > 65536 {
			return fmt.Errorf("evidence ablation case %q ablation_noise_chars must be between 0 and 65536", evalCase.ID)
		}
	default:
		return fmt.Errorf("unsupported eval mode %q", mode)
	}
	return nil
}

func validateAblationTruth(caseID string, truth AblationTruth) error {
	if strings.TrimSpace(truth.RootCause) == "" {
		return fmt.Errorf("evidence ablation case %q truth is missing root_cause", caseID)
	}
	seen := map[string]bool{}
	for _, collection := range [][]AtomicTruth{truth.RequiredFacts, truth.ForbiddenClaims} {
		for _, fact := range collection {
			id := strings.TrimSpace(fact.ID)
			if id == "" || strings.TrimSpace(fact.Description) == "" {
				return fmt.Errorf("evidence ablation case %q has an atomic truth without id or description", caseID)
			}
			if seen[id] {
				return fmt.Errorf("evidence ablation case %q has duplicate atomic truth id %q", caseID, id)
			}
			seen[id] = true
		}
	}
	return nil
}

func RunCase(ctx context.Context, evalCase Case, opts RunOptions, attempt int) (EvalCaseResult, error) {
	startedAt := time.Now()
	conversationID := fmt.Sprintf("eval-%s-%s-%d", sanitizeID(evalCase.ID), opts.RunID, attempt)
	if opts.ContextStressVariant != "" {
		conversationID += "-" + sanitizeID(opts.ContextStressVariant)
	}
	if opts.Mode == ModeEvidenceAblation {
		if !validEvidenceMode(opts.EvidenceMode) {
			return EvalCaseResult{}, fmt.Errorf("unsupported evidence mode %q", opts.EvidenceMode)
		}
		conversationID += "-" + sanitizeID(string(opts.EvidenceMode))
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
	if opts.Mode == ModeEvidenceAblation {
		noiseChars := preparedCase.AblationNoiseChars
		if noiseChars == 0 && strings.EqualFold(strings.TrimSpace(preparedCase.Category), "long_context") {
			noiseChars = 24000
		}
		preparedCase.Context.DurableMemory += ablationContextNoise(noiseChars)
	}

	userMessage := buildUserMessage(preparedCase, conversationID)
	result := EvalCaseResult{
		ID:             evalCase.ID,
		Attempt:        attempt,
		Variant:        opts.ContextStressVariant,
		EvidenceMode:   opts.EvidenceMode,
		PairID:         fmt.Sprintf("%s/%d", evalCase.ID, attempt),
		Category:       evalCase.Category,
		Mode:           opts.Mode,
		Query:          evalCase.Query,
		ConversationID: conversationID,
		StartedAt:      startedAt,
		ModelVersion:   configuredEvalModelVersion(ctx, opts),
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
	if opts.Mode == ModeEvidenceAblation {
		result.ReviewID = "review-" + digestText(opts.RunID + "|" + result.PairID + "|" + string(opts.EvidenceMode))[:16]
		result.Pilot = attempt <= 5
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
	payloads := &payloadRecorder{}
	agentOptions := optionsForCase(preparedCase, opts, stressModel, pressureModel, payloads)
	recorder := trace.NewRecorder()
	runner, err := execution.BuildChatAgentWithOptions(ctx, agentOptions)
	if err != nil {
		result.Error = err.Error()
		finishResult(&result, recorder, startedAt)
		recordPayloads(&result, payloads)
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
		pipelineResult := &chatanswer.ClaimPipelineResult{}
		claimExtractor := structuredClaimExtractorForCase(preparedCase, opts)
		finalAnswer, convergeErr := chatanswer.ConvergeAnswer(ctx, chatanswer.AnswerConvergeInput{
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
	recordPayloads(&result, payloads)
	recordContextStressDiagnostics(&result, preparedCase, stressModel)
	recordQueryResultPressureDiagnostics(&result, preparedCase, pressureModel)
	if opts.EnableJudge {
		result.Judge = JudgeCase(ctx, evalCase, result)
	}
	if opts.Mode == ModeEvidenceAblation {
		normalized := normalizeAblationClaims(evalCase, result)
		if opts.EnableJudge {
			modelNormalized, normalizeErr := normalizeAblationClaimsWithModel(ctx, evalCase, result)
			if normalizeErr == nil {
				normalized = modelNormalized
			} else {
				normalized.NormalizerError = normalizeErr.Error()
				normalized.Normalizer = "deterministic_atomic_v1_fallback"
			}
		}
		result.NormalizedClaims = &normalized
		score := scoreAblationCase(evalCase, result, normalized)
		result.AblationScore = &score
	}
	result.Scores = ScoreCase(evalCase, result)
	return result, nil
}

func ablationContextNoise(maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	unit := " UNRELATED_ARCHIVE inventory cache eviction; shipping queue delay; DNS maintenance; certificate rotation; image resize backlog."
	var builder strings.Builder
	for builder.Len() < maxChars {
		builder.WriteString(unit)
	}
	value := builder.String()
	if len(value) > maxChars {
		value = value[:maxChars]
	}
	return value
}

func shouldRunStructuredClaimPipeline(evalCase Case, opts RunOptions) bool {
	if opts.Mode == ModeEvidenceAblation {
		return opts.EvidenceMode == EvidenceModeFull && (opts.RealModel || evalCase.Claims != nil)
	}
	if evalCase.Claims != nil {
		return true
	}
	return opts.RealModel && opts.Mode == ModeContextStress
}

func structuredClaimExtractorForCase(evalCase Case, opts RunOptions) chatanswer.ClaimExtractor {
	if opts.RealModel || evalCase.Claims == nil {
		return nil
	}
	return fixtureClaimExtractor{claimSet: *evalCase.Claims}
}

func buildUserMessage(evalCase Case, conversationID string) *execution.UserMessage {
	return &execution.UserMessage{
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

func optionsForCase(evalCase Case, opts RunOptions, stressModel *contextStressModel, pressureModel *queryResultPressureModel, payloads *payloadRecorder) execution.ChatAgentOptions {
	switch opts.Mode {
	case ModeContract:
		noRetries := 0
		var failoverModel *scriptedModel
		if len(evalCase.FailoverModelSteps) > 0 {
			failoverModel = newScriptedModel(evalCase.FailoverModelSteps)
		}
		return execution.ChatAgentOptions{
			ChatModel:     newScriptedModel(evalCase.ModelSteps),
			FailoverModel: failoverModel,
			Tools:         fixtureTools(evalCase.ToolFixtures),
			MaxIterations: len(evalCase.ModelSteps) + len(evalCase.FailoverModelSteps) + 2,
			ModelRetries:  &noRetries,
		}
	case ModeRouting:
		return execution.ChatAgentOptions{
			Tools:         fixtureTools(evalCase.ToolFixtures),
			MaxIterations: 8,
		}
	case ModeContextStress:
		noRetries := 0
		spec := normalizeContextStressSpec(evalCase.ContextStress)
		profile := contextStressProfile(spec, opts.ContextStressVariant)
		options := execution.ChatAgentOptions{
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
		options := execution.ChatAgentOptions{
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
	case ModeEvidenceAblation:
		noRetries := 0
		options := execution.ChatAgentOptions{
			Tools:             fixtureToolsForMode(evalCase.ToolFixtures, opts.EvidenceMode, payloads),
			MaxIterations:     8,
			ModelRetries:      &noRetries,
			DisableToolSearch: true,
			EvalEvidenceMode:  string(opts.EvidenceMode),
			AgentScope:        evidenceAblationAgentScope(evalCase.ToolFixtures),
		}
		if !opts.RealModel {
			options.ChatModel = newScriptedModel(evalCase.ModelSteps)
			options.MaxIterations = len(evalCase.ModelSteps) + 2
		}
		return options
	default:
		return execution.ChatAgentOptions{}
	}
}

// Database tools are intentionally hidden from the coordinator in production.
// A database-only eval fixture therefore runs with the same narrow database
// scope that a real database sub-agent would receive.
func evidenceAblationAgentScope(fixtures []ToolFixture) authz.AgentScope {
	if len(fixtures) == 0 {
		return ""
	}
	for _, fixture := range fixtures {
		switch strings.TrimSpace(fixture.Name) {
		case "db_server_health", "db_active_queries", "db_wait_events", "db_locks", "db_slow_queries":
			// Keep checking: mixed-domain fixture sets stay in coordinator scope.
		default:
			return ""
		}
	}
	return authz.AgentScopeDatabase
}

func validEvidenceMode(mode EvidenceMode) bool {
	for _, candidate := range EvidenceModes() {
		if mode == candidate {
			return true
		}
	}
	return false
}

func recordPayloads(result *EvalCaseResult, payloads *payloadRecorder) {
	if result == nil || payloads == nil {
		return
	}
	result.CanonicalPayloadDigest,
		result.ModelVisiblePayloadDigest,
		result.ModelVisiblePayloadBytes,
		result.ModelVisiblePayloadTokens = payloads.Snapshot()
}

func configuredEvalModelVersion(ctx context.Context, opts RunOptions) string {
	if strings.TrimSpace(opts.ModelVersion) != "" {
		return strings.TrimSpace(opts.ModelVersion)
	}
	value, err := g.Cfg().Get(ctx, "quick_chat_model.model")
	if err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		return strings.TrimSpace(value.String())
	}
	if opts.RealModel {
		return "configured-model"
	}
	return "scripted-model"
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
		errorText := ""
		if item.Envelope.Status != evidence.StatusSuccess {
			errorText = item.Envelope.Summary
		}
		result = append(result, ToolEvidence{
			Name:       item.ToolName,
			Input:      item.InputJSON,
			Output:     item.OutputJSON,
			Status:     item.Status(),
			Error:      errorText,
			EvidenceID: item.EvidenceID(),
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
		if call.Name == "mechanism_agent" || call.Name == "knowledge_agent" || call.Name == "query_internal_docs" {
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
		Status   string `json:"status"`
		Error    string `json:"error"`
		Summary  string `json:"summary"`
		Evidence *struct {
			EvidenceID string `json:"evidence_id"`
		} `json:"evidence"`
	}
	if json.Unmarshal([]byte(normalizedOutput), &envelope) != nil {
		return "success", "", ""
	}
	evidenceID = ""
	if envelope.Evidence != nil {
		evidenceID = envelope.Evidence.EvidenceID
	}
	if !strings.EqualFold(envelope.Status, evidence.StatusSuccess) {
		if envelope.Summary == "" {
			envelope.Summary = envelope.Error
		}
		return "error", envelope.Summary, evidenceID
	}
	return "success", "", evidenceID
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
