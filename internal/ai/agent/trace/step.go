package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	aimodels "github.com/Thadopz/telltale/internal/ai/models"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/Thadopz/telltale/utility/mem"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type componentSpanKey struct{}

type AgentStep struct {
	Index                 int       `json:"index"`
	Agent                 string    `json:"agent"`
	Phase                 string    `json:"phase"`
	Component             string    `json:"component,omitempty"`
	Type                  string    `json:"type,omitempty"`
	Name                  string    `json:"name,omitempty"`
	Input                 string    `json:"input,omitempty"`
	Output                string    `json:"output,omitempty"`
	Error                 string    `json:"error,omitempty"`
	ElapsedMS             int64     `json:"elapsed_ms,omitempty"`
	PromptTokens          int       `json:"prompt_tokens,omitempty"`
	CachedPromptTokens    int       `json:"cached_prompt_tokens,omitempty"`
	UncachedPromptTokens  int       `json:"uncached_prompt_tokens,omitempty"`
	CacheHitRatio         float64   `json:"cache_hit_ratio,omitempty"`
	ProviderCacheEnabled  bool      `json:"provider_cache_enabled,omitempty"`
	ProviderCacheFallback bool      `json:"provider_cache_fallback,omitempty"`
	CacheKeyFingerprint   string    `json:"cache_key_fingerprint,omitempty"`
	EstimatedPromptTokens int       `json:"estimated_prompt_tokens,omitempty"`
	CompletionTokens      int       `json:"completion_tokens,omitempty"`
	TotalTokens           int       `json:"total_tokens,omitempty"`
	InputBudgetTokens     int       `json:"input_budget_tokens,omitempty"`
	FinalBudgetTokens     int       `json:"final_budget_tokens,omitempty"`
	ContextWindowTokens   int       `json:"context_window_tokens,omitempty"`
	PromptOverheadTokens  int       `json:"prompt_overhead_tokens,omitempty"`
	SafetyMarginTokens    int       `json:"safety_margin_tokens,omitempty"`
	CalibrationFactor     float64   `json:"calibration_factor,omitempty"`
	TrimmedLayers         []string  `json:"trimmed_layers,omitempty"`
	ClaimSchemaVersion    int       `json:"claim_schema_version,omitempty"`
	ClaimCount            int       `json:"claim_count,omitempty"`
	ClaimExtractorSuccess bool      `json:"claim_extractor_success,omitempty"`
	ClaimExtractorError   string    `json:"claim_extractor_error,omitempty"`
	CoverageStatuses      []string  `json:"coverage_statuses,omitempty"`
	CoveredClaimCount     int       `json:"covered_claim_count,omitempty"`
	SupportedClaimCount   int       `json:"supported_claim_count,omitempty"`
	RefutedClaimCount     int       `json:"refuted_claim_count,omitempty"`
	PartialClaimCount     int       `json:"partial_claim_count,omitempty"`
	UnknownClaimCount     int       `json:"unknown_claim_count,omitempty"`
	UncoveredClaimCount   int       `json:"uncovered_claim_count,omitempty"`
	ConflictClaimCount    int       `json:"conflict_claim_count,omitempty"`
	EvidenceConflictCount int       `json:"evidence_conflict_count,omitempty"`
	LocalizationMode      string    `json:"localization_mode,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
}

type ToolEvidence struct {
	ToolName   string            `json:"tool_name"`
	InputJSON  string            `json:"input_json,omitempty"`
	OutputJSON string            `json:"output_json,omitempty"`
	Envelope   evidence.Envelope `json:"envelope"`
}

func (item ToolEvidence) EvidenceID() string {
	if record, ok := item.Envelope.UsableEvidence(); ok {
		return strings.TrimSpace(record.EvidenceID)
	}
	return ""
}

func (item ToolEvidence) FailedQueryID() string {
	if item.Envelope.Failure == nil {
		return ""
	}
	return strings.TrimSpace(item.Envelope.Failure.QueryID)
}

func (item ToolEvidence) Status() string  { return strings.TrimSpace(item.Envelope.Status) }
func (item ToolEvidence) Summary() string { return strings.TrimSpace(item.Envelope.Summary) }

func (s AgentStep) String() string {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("step=%d agent=%s phase=%s name=%s", s.Index, s.Agent, s.Phase, s.Name)
	}
	return string(b)
}

type Recorder struct {
	mu             sync.Mutex
	steps          []AgentStep
	toolInputs     map[string][]string
	toolEvidence   []ToolEvidence
	modelEstimates map[string][]modelEstimate
}

type modelEstimate struct {
	modelKey  string
	estimated int
}

func NewRecorder() *Recorder {
	return &Recorder{
		toolInputs:     make(map[string][]string),
		modelEstimates: make(map[string][]modelEstimate),
	}
}

func (r *Recorder) Record(step AgentStep) AgentStep {
	if r == nil {
		return step
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	step.Index = len(r.steps) + 1
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Now()
	}
	r.steps = append(r.steps, step)
	return step
}

func (r *Recorder) Steps() []AgentStep {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	steps := make([]AgentStep, len(r.steps))
	copy(steps, r.steps)
	return steps
}

func (r *Recorder) Strings() []string {
	steps := r.Steps()
	res := make([]string, 0, len(steps))
	for _, step := range steps {
		res = append(res, step.String())
	}
	return res
}

func (r *Recorder) ToolEvidence() []ToolEvidence {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	items := make([]ToolEvidence, len(r.toolEvidence))
	copy(items, r.toolEvidence)
	return items
}

func (r *Recorder) QualityCounts() (evidenceCount int, failedToolCount int) {
	for _, item := range r.ToolEvidence() {
		if item.EvidenceID() != "" {
			evidenceCount++
		}
		if item.FailedQueryID() != "" || strings.EqualFold(item.Status(), "error") {
			failedToolCount++
		}
	}
	return evidenceCount, failedToolCount
}

func (r *Recorder) PushToolInput(name string, input string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolInputs == nil {
		r.toolInputs = make(map[string][]string)
	}
	r.toolInputs[name] = append(r.toolInputs[name], input)
}

func (r *Recorder) PopToolInput(name string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.toolInputs[name]
	if len(values) == 0 {
		return ""
	}
	value := values[0]
	r.toolInputs[name] = values[1:]
	return value
}

func (r *Recorder) PushModelEstimate(component string, estimate modelEstimate) {
	if r == nil || estimate.estimated <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.modelEstimates == nil {
		r.modelEstimates = make(map[string][]modelEstimate)
	}
	r.modelEstimates[component] = append(r.modelEstimates[component], estimate)
}

func (r *Recorder) PopModelEstimate(component string) modelEstimate {
	if r == nil {
		return modelEstimate{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.modelEstimates[component]
	if len(values) == 0 {
		return modelEstimate{}
	}
	value := values[0]
	r.modelEstimates[component] = values[1:]
	return value
}

func (r *Recorder) RecordToolEvidence(item ToolEvidence) {
	if r == nil {
		return
	}
	item.ToolName = strings.TrimSpace(item.ToolName)
	if item.ToolName == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolEvidence = append(r.toolEvidence, item)
}

func Callback(agent string, recorder *Recorder) callbacks.Handler {
	builder := callbacks.NewHandlerBuilder()

	builder.OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		ctx = startComponentSpan(ctx, info)
		recordToolInput(info, input, recorder)
		step := AgentStep{
			Agent:     agent,
			Phase:     "start",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Input:     compactJSON(input),
		}
		if info != nil && info.Component == components.ComponentOfChatModel {
			modelKey, estimated := modelCallbackEstimate(ctx, info, input)
			recorder.PushModelEstimate(info.Name, modelEstimate{modelKey: modelKey, estimated: estimated})
			applyBudgetReport(ctx, &step, estimated, modelKey)
		}
		recorder.Record(step)
		return ctx
	})

	builder.OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		recordToolOutput(info, output, recorder)
		promptTokens, cachedTokens, uncachedTokens, completionTokens, totalTokens := modelTokenUsage(info, output)
		cacheTrace := aimodels.ProviderCacheTraceFromContext(ctx)
		step := AgentStep{
			Agent:                 agent,
			Phase:                 "end",
			Component:             string(info.Component),
			Type:                  info.Type,
			Name:                  info.Name,
			Output:                compactJSON(output),
			PromptTokens:          promptTokens,
			CachedPromptTokens:    cachedTokens,
			UncachedPromptTokens:  uncachedTokens,
			CacheHitRatio:         cacheHitRatio(promptTokens, cachedTokens),
			ProviderCacheEnabled:  cacheTrace.Enabled,
			ProviderCacheFallback: cacheTrace.Fallback,
			CacheKeyFingerprint:   cacheTrace.KeyFingerprint,
			CompletionTokens:      completionTokens,
			TotalTokens:           totalTokens,
		}
		if info != nil && info.Component == components.ComponentOfChatModel {
			estimate := recorder.PopModelEstimate(info.Name)
			step.EstimatedPromptTokens = estimate.estimated
			if promptTokens > 0 && estimate.estimated > 0 {
				contextbudget.DefaultCalibrator().Observe(ctx, estimate.modelKey, estimate.estimated, promptTokens)
			}
			step.CalibrationFactor = contextbudget.DefaultCalibrator().Factor(ctx, estimate.modelKey)
			recordModelCacheObservability(ctx, agent, info.Name, false, step)
		}
		recorder.Record(step)
		endComponentSpan(ctx, nil)
		return ctx
	})

	builder.OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil {
			return ctx
		}
		if info.Component == components.ComponentOfChatModel {
			// Do not let a failed attempt's estimate be paired with a later
			// retry's successful PromptTokens. Failed calls never calibrate.
			recorder.PopModelEstimate(info.Name)
		}
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		recordToolError(info, err, recorder)
		recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "error",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Error:     truncate(errText, 1000),
		})
		endComponentSpan(ctx, err)
		return ctx
	})

	return builder.Build()
}

func StreamingCallback(agent string, recorder *Recorder, emit func(event string, step AgentStep)) callbacks.Handler {
	builder := callbacks.NewHandlerBuilder()

	builder.OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		if info == nil {
			return ctx
		}
		ctx = startComponentSpan(ctx, info)
		recordToolInput(info, input, recorder)
		step := AgentStep{
			Agent:     agent,
			Phase:     "start",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Input:     compactJSON(input),
		}
		if info.Component == components.ComponentOfChatModel {
			modelKey, estimated := modelCallbackEstimate(ctx, info, input)
			recorder.PushModelEstimate(info.Name, modelEstimate{modelKey: modelKey, estimated: estimated})
			applyBudgetReport(ctx, &step, estimated, modelKey)
		}
		step = recorder.Record(step)
		emitTraceStep(info, step, emit)
		return ctx
	})

	builder.OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		if info == nil {
			return ctx
		}
		recordToolOutput(info, output, recorder)
		promptTokens, cachedTokens, uncachedTokens, completionTokens, totalTokens := modelTokenUsage(info, output)
		cacheTrace := aimodels.ProviderCacheTraceFromContext(ctx)
		step := AgentStep{
			Agent:                 agent,
			Phase:                 "end",
			Component:             string(info.Component),
			Type:                  info.Type,
			Name:                  info.Name,
			Output:                compactJSON(output),
			PromptTokens:          promptTokens,
			CachedPromptTokens:    cachedTokens,
			UncachedPromptTokens:  uncachedTokens,
			CacheHitRatio:         cacheHitRatio(promptTokens, cachedTokens),
			ProviderCacheEnabled:  cacheTrace.Enabled,
			ProviderCacheFallback: cacheTrace.Fallback,
			CacheKeyFingerprint:   cacheTrace.KeyFingerprint,
			CompletionTokens:      completionTokens,
			TotalTokens:           totalTokens,
		}
		if info.Component == components.ComponentOfChatModel {
			estimate := recorder.PopModelEstimate(info.Name)
			step.EstimatedPromptTokens = estimate.estimated
			if promptTokens > 0 && estimate.estimated > 0 {
				contextbudget.DefaultCalibrator().Observe(ctx, estimate.modelKey, estimate.estimated, promptTokens)
			}
			step.CalibrationFactor = contextbudget.DefaultCalibrator().Factor(ctx, estimate.modelKey)
			recordModelCacheObservability(ctx, agent, info.Name, true, step)
		}
		step = recorder.Record(step)
		emitTraceStep(info, step, emit)
		endComponentSpan(ctx, nil)
		return ctx
	})

	builder.OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil {
			return ctx
		}
		if info.Component == components.ComponentOfChatModel {
			recorder.PopModelEstimate(info.Name)
		}
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		recordToolError(info, err, recorder)
		step := recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "error",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Error:     truncate(errText, 1000),
		})
		emitTraceStep(info, step, emit)
		endComponentSpan(ctx, err)
		return ctx
	})

	return builder.Build()
}

func modelTokenUsage(info *callbacks.RunInfo, output callbacks.CallbackOutput) (prompt int, cached int, uncached int, completion int, total int) {
	if info == nil || info.Component != components.ComponentOfChatModel {
		return 0, 0, 0, 0, 0
	}
	modelOutput := model.ConvCallbackOutput(output)
	if modelOutput == nil || modelOutput.TokenUsage == nil {
		return 0, 0, 0, 0, 0
	}
	prompt = modelOutput.TokenUsage.PromptTokens
	cached = modelOutput.TokenUsage.PromptTokenDetails.CachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > prompt {
		cached = prompt
	}
	uncached = prompt - cached
	return prompt, cached, uncached, modelOutput.TokenUsage.CompletionTokens, modelOutput.TokenUsage.TotalTokens
}

func cacheHitRatio(promptTokens int, cachedTokens int) float64 {
	if promptTokens <= 0 || cachedTokens <= 0 {
		return 0
	}
	return float64(cachedTokens) / float64(promptTokens)
}

func recordModelCacheObservability(ctx context.Context, agent string, modelName string, streaming bool, step AgentStep) {
	cacheSupported := step.CachedPromptTokens > 0 || (step.ProviderCacheEnabled && !step.ProviderCacheFallback)
	observability.RecordModelCache(ctx, agent, modelName, streaming, step.ProviderCacheEnabled, cacheSupported, step.ProviderCacheFallback, step.PromptTokens, step.CachedPromptTokens, step.UncachedPromptTokens)
	span, ok := ctx.Value(componentSpanKey{}).(oteltrace.Span)
	if !ok {
		return
	}
	span.SetAttributes(
		attribute.Int("model.prompt_tokens", step.PromptTokens),
		attribute.Int("model.cached_prompt_tokens", step.CachedPromptTokens),
		attribute.Int("model.uncached_prompt_tokens", step.UncachedPromptTokens),
		attribute.Float64("model.cache_hit_ratio", step.CacheHitRatio),
		attribute.Bool("model.cache_enabled", step.ProviderCacheEnabled),
		attribute.Bool("model.cache_supported", cacheSupported),
		attribute.Bool("model.cache_fallback", step.ProviderCacheFallback),
		attribute.String("model.cache_key_fingerprint", step.CacheKeyFingerprint),
	)
}

func modelCallbackEstimate(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) (string, int) {
	modelInput := model.ConvCallbackInput(input)
	modelKey := "chat_model"
	if info != nil && strings.TrimSpace(info.Name) != "" {
		modelKey = strings.TrimSpace(info.Name)
	}
	if modelInput == nil {
		return contextbudget.CalibrationKeyForModel(ctx, modelKey), 0
	}
	if modelInput.Config != nil && strings.TrimSpace(modelInput.Config.Model) != "" {
		modelKey = strings.TrimSpace(modelInput.Config.Model)
	}
	total := 32
	for _, message := range modelInput.Messages {
		total += mem.EstimateMessageTokens(message)
	}
	for _, toolInfo := range modelInput.Tools {
		if toolInfo == nil {
			continue
		}
		encoded, err := json.Marshal(toolInfo)
		if err == nil {
			total += contextbudget.EstimateTextTokens(string(encoded)) + 4
		}
	}
	return contextbudget.CalibrationKeyForModel(ctx, modelKey), total
}

func applyBudgetReport(ctx context.Context, step *AgentStep, estimated int, modelKey string) {
	if step == nil {
		return
	}
	step.EstimatedPromptTokens = estimated
	step.CalibrationFactor = contextbudget.DefaultCalibrator().Factor(ctx, modelKey)
	if report, ok := contextbudget.BudgetReportFromContext(ctx); ok {
		step.InputBudgetTokens = report.InputLimitTokens
		step.FinalBudgetTokens = report.FinalBudgetTokens
		step.ContextWindowTokens = report.ContextWindowTokens
		step.PromptOverheadTokens = report.PromptOverheadTokens
		step.SafetyMarginTokens = report.SafetyMarginTokens
		step.TrimmedLayers = append([]string(nil), report.TrimmedLayers...)
	}
}

func startComponentSpan(ctx context.Context, info *callbacks.RunInfo) context.Context {
	if info == nil {
		return ctx
	}
	ctx, span := observability.StartSpan(ctx, "eino.component",
		attribute.String("eino.component", string(info.Component)),
		attribute.String("eino.type", info.Type),
		attribute.String("eino.name", info.Name),
	)
	return context.WithValue(ctx, componentSpanKey{}, span)
}

func endComponentSpan(ctx context.Context, err error) {
	span, ok := ctx.Value(componentSpanKey{}).(oteltrace.Span)
	if !ok {
		return
	}
	observability.EndSpan(span, err)
}

func recordToolInput(info *callbacks.RunInfo, input callbacks.CallbackInput, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	toolInput := tool.ConvCallbackInput(input)
	if toolInput == nil {
		return
	}
	recorder.PushToolInput(info.Name, toolInput.ArgumentsInJSON)
}

func recordToolOutput(info *callbacks.RunInfo, output callbacks.CallbackOutput, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	toolOutput := tool.ConvCallbackOutput(output)
	response := ""
	if toolOutput != nil {
		response = toolOutput.Response
	}
	parsed := evidence.Parse(response).Envelope
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName:   info.Name,
		InputJSON:  recorder.PopToolInput(info.Name),
		OutputJSON: response,
		Envelope:   parsed,
	})
}

func recordToolError(info *callbacks.RunInfo, err error, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	env := evidence.NewError(err, "tool callback error")
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName:  info.Name,
		InputJSON: recorder.PopToolInput(info.Name),
		Envelope:  env,
	})
}

func emitTraceStep(info *callbacks.RunInfo, step AgentStep, emit func(event string, step AgentStep)) {
	if emit == nil || info == nil {
		return
	}
	if info.Component != components.ComponentOfTool {
		return
	}
	switch step.Phase {
	case "start":
		emit("tool_start", step)
	case "end":
		emit("tool_result", step)
	case "error":
		emit("tool_error", step)
	}
}

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return truncate(string(b), 2000)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
