package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/input_preprocessor"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/Thadopz/telltale/utility/log_call_back"
	"github.com/Thadopz/telltale/utility/mem"
)

// DiagnoseRequest is the input shared by the CLI and HTTP adapters.
// Identity always comes from configuration and is intentionally not part of
// this request.
type DiagnoseRequest struct {
	Question       string
	ConversationID string
}

// StreamEvent is a safe, already-serialized event for an adapter to display.
// It contains trace metadata and bounded tool summaries, not raw prompts.
type StreamEvent struct {
	Name string
	Data string
}

// EventSink receives streaming events. The CLI does not use this in v1, while
// the HTTP adapter maps events to its existing SSE protocol.
type EventSink func(StreamEvent) error

// DiagnosticService is the application boundary used by all entrypoints.
type DiagnosticService interface {
	Diagnose(context.Context, DiagnoseRequest) (DiagnoseResult, error)
	DiagnoseStream(context.Context, DiagnoseRequest, EventSink) (DiagnoseResult, error)
}

// DiagnoseResult contains structured, bounded output for a completed run.
// Full tool payloads and prompts are deliberately not exposed here.
type DiagnoseResult struct {
	Answer          string                     `json:"answer"`
	RunID           string                     `json:"run_id"`
	ConversationID  string                     `json:"conversation_id"`
	ClaimSet        evidence.ClaimSet          `json:"claim_set"`
	CoverageReports []evidence.CoverageReport  `json:"coverage_reports,omitempty"`
	Hypotheses      []chat_pipeline.Hypothesis `json:"hypotheses,omitempty"`
	EvidenceIDs     []string                   `json:"evidence_ids,omitempty"`
	FailedQueryIDs  []string                   `json:"failed_query_ids,omitempty"`
	TraceSummary    TraceSummary               `json:"trace_summary"`
}

type TraceSummary struct {
	StepCount       int               `json:"step_count"`
	EvidenceCount   int               `json:"evidence_count"`
	FailedToolCount int               `json:"failed_tool_count"`
	ToolCalls       []ToolCallSummary `json:"tool_calls,omitempty"`
	BudgetReports   []BudgetSummary   `json:"budget_reports,omitempty"`
}

type ToolCallSummary struct {
	ToolName       string                      `json:"tool_name"`
	Status         string                      `json:"status"`
	Summary        string                      `json:"summary,omitempty"`
	EvidenceID     string                      `json:"evidence_id,omitempty"`
	FailedQueryID  string                      `json:"failed_query_id,omitempty"`
	DataStatus     string                      `json:"data_status,omitempty"`
	SourceKind     evidence.EvidenceSourceKind `json:"source_kind,omitempty"`
	ResultMetadata evidence.ResultMetadata     `json:"result_metadata,omitempty"`
}

type BudgetSummary struct {
	EstimatedPromptTokens int      `json:"estimated_prompt_tokens,omitempty"`
	PromptTokens          int      `json:"prompt_tokens,omitempty"`
	FinalBudgetTokens     int      `json:"final_budget_tokens,omitempty"`
	ContextWindowTokens   int      `json:"context_window_tokens,omitempty"`
	TrimmedLayers         []string `json:"trimmed_layers,omitempty"`
}

type service struct{}

func NewService() DiagnosticService {
	return &service{}
}

type preparedTurn struct {
	Context        context.Context
	UserID         string
	ConversationID string
	Question       string
	Memory         *mem.SimpleMemory
	UserMessage    *chat_pipeline.UserMessage
}

func (s *service) Diagnose(ctx context.Context, request DiagnoseRequest) (DiagnoseResult, error) {
	turn, err := prepareTurn(ctx, request)
	if err != nil {
		return DiagnoseResult{}, err
	}

	runner, err := chat_pipeline.BuildChatAgent(turn.Context)
	if err != nil {
		return DiagnoseResult{}, fmt.Errorf("build chat agent: %w", err)
	}

	recorder := trace.NewRecorder()
	callbackHandlers := defaultCallbacks(turn, recorder)
	out, state, err := chat_pipeline.RunDiagnosticLoopWithState(
		turn.Context,
		runner,
		turn.UserMessage,
		recorder,
		compose.WithCallbacks(callbackHandlers...),
	)
	if err != nil {
		return DiagnoseResult{}, err
	}

	return finishTurn(turn.Context, turn, out, state, recorder)
}

func (s *service) DiagnoseStream(ctx context.Context, request DiagnoseRequest, sink EventSink) (DiagnoseResult, error) {
	turn, err := prepareTurn(ctx, request)
	if err != nil {
		return DiagnoseResult{}, err
	}

	runner, err := chat_pipeline.BuildChatAgent(turn.Context)
	if err != nil {
		return DiagnoseResult{}, fmt.Errorf("build chat agent: %w", err)
	}

	recorder := trace.NewRecorder()
	startedAt := time.Now()
	if err := emit(sink, "thinking", `{"message":"analyzing request"}`); err != nil {
		return DiagnoseResult{}, err
	}

	state := chat_pipeline.BuildDiagnosticState(turn.Question)
	diagnosticTurn := chat_pipeline.PrepareDiagnosticTurn(turn.UserMessage, state)
	callbackHandlers := []callbacks.Handler{
		trace.StreamingCallback("chat_stream_agent", recorder, func(event string, step trace.AgentStep) {
			data, marshalErr := json.Marshal(step)
			if marshalErr != nil {
				return
			}
			_ = emit(sink, event, string(data))
		}),
		mem.ToolResultCallback(turn.UserID, turn.ConversationID),
	}
	if !authz.QuietLogging(turn.Context) {
		callbackHandlers = append([]callbacks.Handler{log_call_back.LogCallback(nil)}, callbackHandlers...)
	}
	callbackOptions := compose.WithCallbacks(callbackHandlers...)

	stream, err := runner.Stream(turn.Context, diagnosticTurn, callbackOptions)
	if err != nil {
		return DiagnoseResult{}, err
	}
	defer stream.Close()

	var fullResponse strings.Builder
	firstTokenRecorded := false
	for {
		chunk, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			originalResponse := fullResponse.String()
			chat_pipeline.UpdateDiagnosticState(&state, recorder.ToolEvidence())
			if chat_pipeline.NeedsDiagnosticFollowUp(state) {
				followUpTurn := chat_pipeline.PrepareDiagnosticFollowUp(turn.UserMessage, &state, recorder.ToolEvidence())
				followUp, followUpErr := runner.Invoke(turn.Context, followUpTurn, callbackOptions)
				if followUpErr != nil {
					g.Log().Warningf(turn.Context, "diagnostic follow-up failed; keep first answer: %v", followUpErr)
				} else if followUp != nil && strings.TrimSpace(followUp.Content) != "" {
					originalResponse = followUp.Content
					if err := emit(sink, "replace_message", marshalStreamContent(originalResponse)); err != nil {
						return DiagnoseResult{}, err
					}
				}
			}

			result, finishErr := finishTurnWithAnswer(turn.Context, turn, originalResponse, state, recorder)
			if finishErr != nil {
				return DiagnoseResult{}, finishErr
			}
			if result.Answer != originalResponse {
				if err := emit(sink, "replace_message", marshalStreamContent(result.Answer)); err != nil {
					return DiagnoseResult{}, err
				}
			}
			if err := emit(sink, "final_message", marshalStreamContent(result.Answer)); err != nil {
				return DiagnoseResult{}, err
			}
			if err := emit(sink, "message", result.Answer); err != nil {
				return DiagnoseResult{}, err
			}
			if err := emit(sink, "done", "Stream completed"); err != nil {
				return DiagnoseResult{}, err
			}
			return result, nil
		}
		if receiveErr != nil {
			return DiagnoseResult{}, receiveErr
		}

		fullResponse.WriteString(chunk.Content)
		if chunk.Content == "" {
			continue
		}
		if !firstTokenRecorded {
			observability.RecordFirstToken(turn.Context, time.Since(startedAt))
			firstTokenRecorded = true
		}
		if err := emit(sink, "draft_message", chunk.Content); err != nil {
			return DiagnoseResult{}, err
		}
	}
}

func defaultCallbacks(turn *preparedTurn, recorder *trace.Recorder) []callbacks.Handler {
	handlers := []callbacks.Handler{
		trace.Callback("chat_agent", recorder),
		mem.ToolResultCallback(turn.UserID, turn.ConversationID),
	}
	if !authz.QuietLogging(turn.Context) {
		handlers = append([]callbacks.Handler{log_call_back.LogCallback(nil)}, handlers...)
	}
	return handlers
}

func prepareTurn(ctx context.Context, request DiagnoseRequest) (*preparedTurn, error) {
	request, err := validateDiagnoseRequest(request)
	if err != nil {
		return nil, err
	}
	question := request.Question

	principal := authz.ConfiguredPrincipal(ctx)
	if strings.TrimSpace(principal.UserID) == "" {
		return nil, fmt.Errorf("identity.user_id is required")
	}

	runID := evidence.NewRunID()
	conversationID := request.ConversationID
	if conversationID == "" {
		conversationID = "cli-" + runID
	}

	ctx = authz.WithPrincipal(ctx, principal)
	ctx = context.WithValue(ctx, "client_id", conversationID)
	ctx = evidence.WithRunID(ctx, runID)

	memory := mem.GetConversationMemory(principal.UserID, conversationID)
	memory.SetCompactionPolicy(mem.ConfiguredCompactionPolicy(ctx))
	layers, err := mem.LoadContextLayers(ctx, principal.UserID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("load chat context: %w", err)
	}

	return &preparedTurn{
		Context:        ctx,
		UserID:         principal.UserID,
		ConversationID: conversationID,
		Question:       question,
		Memory:         memory,
		UserMessage: &chat_pipeline.UserMessage{
			ID:             conversationID,
			UserID:         principal.UserID,
			ConversationID: conversationID,
			Query:          question,
			History:        memory.GetContextMessages(chat_pipeline.DynamicContextPolicy(ctx)),
			Summary:        memory.GetContextSummary(mem.ContextSummaryMaxChars),
			SessionState:   layers.SessionState,
			DurableMemory:  layers.DurableMemory,
			ToolResults:    layers.ToolResults,
		},
	}, nil
}

func validateDiagnoseRequest(request DiagnoseRequest) (DiagnoseRequest, error) {
	question := strings.TrimSpace(request.Question)
	if question == "" {
		return DiagnoseRequest{}, fmt.Errorf("question is required")
	}
	if !utf8.ValidString(question) {
		return DiagnoseRequest{}, fmt.Errorf("question must be valid UTF-8")
	}
	if utf8.RuneCountInString(question) > input_preprocessor.DefaultMaxQueryChars {
		return DiagnoseRequest{}, fmt.Errorf("question exceeds the %d character limit", input_preprocessor.DefaultMaxQueryChars)
	}
	question = input_preprocessor.CleanQuery(question, input_preprocessor.DefaultMaxQueryChars)
	if question == "" {
		return DiagnoseRequest{}, fmt.Errorf("question is empty after removing control characters")
	}

	conversationID := strings.TrimSpace(request.ConversationID)
	if conversationID != "" {
		if utf8.RuneCountInString(conversationID) > 128 {
			return DiagnoseRequest{}, fmt.Errorf("conversation_id exceeds the 128 character limit")
		}
		for _, character := range conversationID {
			if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("-_.:@", character) {
				continue
			}
			return DiagnoseRequest{}, fmt.Errorf("conversation_id contains an invalid character")
		}
	}

	return DiagnoseRequest{
		Question:       question,
		ConversationID: conversationID,
	}, nil
}

func finishTurn(ctx context.Context, turn *preparedTurn, answer *schema.Message, state chat_pipeline.DiagnosticState, recorder *trace.Recorder) (DiagnoseResult, error) {
	if answer == nil {
		return DiagnoseResult{}, fmt.Errorf("diagnostic loop returned no answer")
	}
	return finishTurnWithAnswer(ctx, turn, answer.Content, state, recorder)
}

func finishTurnWithAnswer(ctx context.Context, turn *preparedTurn, draftAnswer string, state chat_pipeline.DiagnosticState, recorder *trace.Recorder) (DiagnoseResult, error) {
	pipelineResult := &chat_pipeline.ClaimPipelineResult{}
	answer, err := chat_pipeline.ConvergeAnswer(ctx, chat_pipeline.AnswerConvergeInput{
		Question:        turn.Question,
		Answer:          draftAnswer,
		Mode:            chat_pipeline.ResponseModeDiagnostic,
		DiagnosticState: &state,
		Steps:           recorder.Steps(),
		ToolEvidence:    recorder.ToolEvidence(),
		Recorder:        recorder,
		PipelineResult:  pipelineResult,
	})
	if err != nil {
		return DiagnoseResult{}, err
	}

	if err := saveCompletedTurn(ctx, turn, answer, pipelineResult); err != nil {
		return DiagnoseResult{}, err
	}

	return buildResult(ctx, turn, answer, state, recorder, pipelineResult), nil
}

func saveCompletedTurn(ctx context.Context, turn *preparedTurn, answer string, pipelineResult *chat_pipeline.ClaimPipelineResult) error {
	turn.Memory.SetMessages(schema.UserMessage(turn.Question))
	turn.Memory.SetMessages(schema.AssistantMessage(answer, nil))
	if pipelineResult != nil {
		turn.Memory.AddContextFacts(contextFactsFromPipeline(pipelineResult))
	}

	if err := mem.UpdateSessionState(ctx, turn.UserID, turn.ConversationID, turn.Question, answer); err != nil {
		return fmt.Errorf("update chat session state: %w", err)
	}
	if err := mem.ExtractAndSaveDurableMemories(ctx, turn.UserID, turn.ConversationID, turn.Question, answer); err != nil {
		g.Log().Warningf(ctx, "durable memory extraction failed user_id=%s conversation_id=%s err=%v", turn.UserID, turn.ConversationID, err)
	}
	return nil
}

func contextFactsFromPipeline(result *chat_pipeline.ClaimPipelineResult) []mem.ContextFact {
	if result == nil {
		return nil
	}
	facts := make([]mem.ContextFact, 0)
	for _, claim := range result.ClaimSet.Claims {
		facts = append(facts,
			mem.ContextFact{Kind: "claim_id", Value: claim.ClaimID, SourceRole: "claim_pipeline"},
			mem.ContextFact{Kind: "claim_type", Value: claim.ClaimID + "=" + claim.ClaimType, SourceRole: "claim_pipeline"},
			mem.ContextFact{Kind: "assertion_kind", Value: claim.ClaimID + "=" + claim.AssertionKind, SourceRole: "claim_pipeline"},
		)
		for _, evidenceID := range claim.EvidenceIDs {
			facts = append(facts, mem.ContextFact{Kind: "evidence_id", Value: evidenceID, SourceRole: "claim_pipeline"})
		}
		for _, failedQueryID := range claim.FailedQueryIDs {
			facts = append(facts, mem.ContextFact{Kind: "failed_query_id", Value: failedQueryID, SourceRole: "claim_pipeline"})
		}
	}
	for _, report := range result.CoverageReports {
		facts = append(facts, mem.ContextFact{Kind: "coverage_status", Value: report.Claim.ClaimID + "=" + report.Status, SourceRole: "claim_matcher"})
		for _, evidenceID := range report.MatchedEvidenceIDs {
			facts = append(facts, mem.ContextFact{Kind: "evidence_id", Value: evidenceID, SourceRole: "claim_matcher"})
		}
		for _, failedQueryID := range report.FailedQueryIDs {
			facts = append(facts, mem.ContextFact{Kind: "failed_query_id", Value: failedQueryID, SourceRole: "claim_matcher"})
		}
	}
	for _, hypothesis := range result.Hypotheses {
		facts = append(facts,
			mem.ContextFact{Kind: "hypothesis_id", Value: hypothesis.ID, SourceRole: "diagnostic_state"},
			mem.ContextFact{Kind: "hypothesis_status", Value: hypothesis.ID + "=" + string(hypothesis.Status), SourceRole: "diagnostic_state"},
		)
		for _, evidenceID := range hypothesis.EvidenceIDs {
			facts = append(facts, mem.ContextFact{Kind: "evidence_id", Value: evidenceID, SourceRole: "diagnostic_state"})
		}
	}
	return facts
}

func buildResult(ctx context.Context, turn *preparedTurn, answer string, state chat_pipeline.DiagnosticState, recorder *trace.Recorder, pipelineResult *chat_pipeline.ClaimPipelineResult) DiagnoseResult {
	result := DiagnoseResult{
		Answer:         answer,
		RunID:          evidence.RunIDFromContext(ctx),
		ConversationID: turn.ConversationID,
		Hypotheses:     append([]chat_pipeline.Hypothesis(nil), state.Hypotheses...),
		TraceSummary:   summarizeTrace(recorder),
	}
	if pipelineResult != nil {
		result.ClaimSet = pipelineResult.ClaimSet
		result.CoverageReports = append([]evidence.CoverageReport(nil), pipelineResult.CoverageReports...)
	}
	for _, item := range recorder.ToolEvidence() {
		if item.EvidenceID != "" {
			result.EvidenceIDs = appendUnique(result.EvidenceIDs, item.EvidenceID)
		}
		if item.FailedQueryID != "" {
			result.FailedQueryIDs = appendUnique(result.FailedQueryIDs, item.FailedQueryID)
		}
	}
	return result
}

func summarizeTrace(recorder *trace.Recorder) TraceSummary {
	if recorder == nil {
		return TraceSummary{}
	}
	items := recorder.ToolEvidence()
	summary := TraceSummary{
		StepCount:       len(recorder.Steps()),
		EvidenceCount:   0,
		FailedToolCount: 0,
		ToolCalls:       make([]ToolCallSummary, 0, len(items)),
		BudgetReports:   make([]BudgetSummary, 0),
	}
	for _, item := range items {
		if item.EvidenceID != "" {
			summary.EvidenceCount++
		}
		if item.FailedQueryID != "" || strings.EqualFold(item.Status, "error") {
			summary.FailedToolCount++
		}
		summary.ToolCalls = append(summary.ToolCalls, ToolCallSummary{
			ToolName:       item.ToolName,
			Status:         item.Status,
			Summary:        item.Summary,
			EvidenceID:     item.EvidenceID,
			FailedQueryID:  item.FailedQueryID,
			DataStatus:     item.Envelope.DataStatus,
			SourceKind:     item.Envelope.SourceKind,
			ResultMetadata: item.Envelope.ResultMetadata,
		})
	}
	for _, step := range recorder.Steps() {
		if step.PromptTokens == 0 && step.EstimatedPromptTokens == 0 && step.FinalBudgetTokens == 0 {
			continue
		}
		summary.BudgetReports = append(summary.BudgetReports, BudgetSummary{
			EstimatedPromptTokens: step.EstimatedPromptTokens,
			PromptTokens:          step.PromptTokens,
			FinalBudgetTokens:     step.FinalBudgetTokens,
			ContextWindowTokens:   step.ContextWindowTokens,
			TrimmedLayers:         append([]string(nil), step.TrimmedLayers...),
		})
	}
	return summary
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func emit(sink EventSink, name string, data string) error {
	if sink == nil {
		return nil
	}
	return sink(StreamEvent{Name: name, Data: data})
}

func marshalStreamContent(content string) string {
	b, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return `{"content":""}`
	}
	return string(b)
}
