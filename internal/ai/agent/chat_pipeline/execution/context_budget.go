package execution

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/utility/mem"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	// These values are compatibility defaults for callers that assemble a
	// ModelContext without building a complete chat graph. The actual request
	// budget is calculated from the configured model profile.
	HistoryMaxChars              = 8000
	SummaryMaxChars              = 3000
	SessionStateMaxChars         = 6000
	DurableMemoryMaxChars        = 2000
	ToolResultsMaxChars          = 2500
	DefaultModelContextMaxTokens = contextbudget.DefaultWorkingPromptCap
)

const (
	historyShare      = 0.27
	summaryShare      = 0.13
	sessionShare      = 0.06
	durableShare      = 0.20
	toolResultsShare  = 0.17
	projectShare      = 0.10
	skillsShare       = 0.07
	minimumLayerToken = 256
)

type contextBudgetPlan struct {
	Profile        contextbudget.ModelContextProfile
	PromptOverhead int
}

func contextBudgetPlanForOptions(ctx context.Context, opts ChatAgentOptions, promptOverhead int) contextBudgetPlan {
	plan := defaultContextBudgetPlan(ctx)
	if opts.ContextProfile != nil {
		plan.Profile = *opts.ContextProfile
	}
	if opts.FailoverContextProfile != nil {
		plan.Profile = contextbudget.MergeProfiles(plan.Profile, opts.FailoverContextProfile)
	} else if opts.FailoverModel != nil {
		// A fake or injected failover model still needs the same safe
		// intersection as a configured failover model. If the caller does not
		// provide an explicit profile, use the configured failover role.
		backup := contextbudget.ConfiguredProfile(ctx, "failover_chat_model")
		plan.Profile = contextbudget.MergeProfiles(plan.Profile, &backup)
	}
	if promptOverhead > plan.PromptOverhead {
		plan.PromptOverhead = promptOverhead
	}
	if opts.PromptOverheadTokens > plan.PromptOverhead {
		plan.PromptOverhead = opts.PromptOverheadTokens
	}
	return plan
}

func defaultContextBudgetPlan(ctx context.Context) contextBudgetPlan {
	primary := contextbudget.ConfiguredProfile(ctx, "quick_chat_model")
	var failover *contextbudget.ModelContextProfile
	if value, err := g.Cfg().Get(ctx, "agent.failover.enabled"); err == nil && value != nil && value.Bool() {
		backup := contextbudget.ConfiguredProfile(ctx, "failover_chat_model")
		failover = &backup
	}

	profile := contextbudget.MergeProfiles(primary, failover)
	overhead := contextbudget.EstimateStaticPrompt(coordinatorSystemPrompt())
	if configured := configuredPromptOverhead(ctx); configured > overhead {
		overhead = configured
	}
	return contextBudgetPlan{Profile: profile, PromptOverhead: overhead}
}

func configuredPromptOverhead(ctx context.Context) int {
	if value, err := g.Cfg().Get(ctx, "context.prompt_overhead_tokens"); err == nil && value != nil && value.Int() > 0 {
		return value.Int()
	}
	return 0
}

func (p contextBudgetPlan) contentBudget(ctx context.Context, estimated int) (contextbudget.PromptBudget, error) {
	budget, _, err := p.allocations(ctx, estimated)
	return budget, err
}

func (p contextBudgetPlan) allocations(ctx context.Context, estimated int) (contextbudget.PromptBudget, contextbudget.ToolAllocation, error) {
	key := contextbudget.CalibrationKey(p.Profile)
	factor := contextbudget.DefaultCalibrator().Factor(ctx, key)
	budget, err := contextbudget.Calculate(p.Profile, p.PromptOverhead, estimated, factor)
	if err != nil {
		return budget, contextbudget.ToolAllocation{}, err
	}
	limits := contextbudget.AllocateTools(int(math.Floor(float64(budget.ContentBudgetTokens) / factor)))
	budget.ContentBudgetTokens -= int(math.Ceil(float64(limits.TotalTokens) * factor))
	return budget, limits, nil
}

func (p contextBudgetPlan) withToolBudget(ctx context.Context) (context.Context, error) {
	_, limits, err := p.allocations(ctx, 0)
	if err != nil {
		return ctx, err
	}
	return contextbudget.WithToolAllocation(ctx, limits), nil
}

func trimRunesHead(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[len(runes)-max:])
}

// budgetHistoryMessages keeps the newest complete user/assistant messages
// while applying a character budget. The aggregate token budget is applied
// later after all context layers have been loaded.
func budgetHistoryMessages(history []*schema.Message, maxChars int) []*schema.Message {
	if len(history) == 0 {
		return nil
	}
	if maxChars <= 0 {
		return []*schema.Message{}
	}

	cloned := cloneHistory(history)
	total := 0
	start := len(cloned)
	for i := len(cloned) - 1; i >= 0; i-- {
		if cloned[i] == nil {
			continue
		}
		nextTotal := total + utf8.RuneCountInString(cloned[i].Content)
		if nextTotal > maxChars && start < len(cloned) {
			break
		}
		total = nextTotal
		start = i
	}
	if start >= len(cloned) {
		return []*schema.Message{}
	}

	start = alignHistoryPairStart(cloned, start)
	if start >= len(cloned) {
		return []*schema.Message{}
	}

	budgeted := cloneHistory(cloned[start:])
	total = 0
	for _, msg := range budgeted {
		if msg == nil {
			continue
		}
		total += utf8.RuneCountInString(msg.Content)
	}
	if total > maxChars && len(budgeted) > 0 {
		excess := total - maxChars
		first := budgeted[0]
		firstLen := utf8.RuneCountInString(first.Content)
		if firstLen > excess {
			first.Content = trimRunesHead(first.Content, firstLen-excess)
		}
	}
	return budgeted
}

// budgetModelContext is kept as a compatibility wrapper for tests and small
// callers. The request graph uses budgetModelContextWithPlan so it can include
// the actual tool and agent prompt overhead.
func budgetModelContext(ctx context.Context, modelContext ModelContext) ModelContext {
	planned, err := budgetModelContextWithPlan(ctx, modelContext, defaultContextBudgetPlan(ctx))
	if err != nil {
		modelContext.BudgetError = err.Error()
	}
	return planned
}

func budgetModelContextWithPlan(ctx context.Context, modelContext ModelContext, plan contextBudgetPlan) (ModelContext, error) {
	estimated := estimateModelContextTokens(modelContext)
	trimmed := uniqueStrings(modelContext.Budget.TrimmedLayers)
	softTrimmed := applySoftLayerBudgets(ctx, &modelContext, plan)
	trimmed = uniqueStrings(append(trimmed, softTrimmed...))
	budget, limits, err := plan.allocations(ctx, estimated)
	if err != nil {
		return modelContext, err
	}

	mandatoryTokens := mem.EstimateTextTokens(modelContext.Content) + mem.EstimateTextTokens(modelContext.RequestAnalysis)
	if mandatoryTokens > budget.ContentBudgetTokens {
		return modelContext, fmt.Errorf("context_budget_exceeded: current request requires %d content tokens, available %d", mandatoryTokens, budget.ContentBudgetTokens)
	}

	modelContext.BudgetError = ""
	modelContext.Budget = contextbudget.BudgetReport{
		ToolReserveTokens:     limits.TotalTokens,
		ToolOutputTokens:      limits.OutputTokens,
		KnowledgeTargetTokens: limits.SelectionTarget(),
		EstimatedTokens:       estimated,
		EffectiveEstimated:    budget.EffectiveEstimatedTokens,
		FinalBudgetTokens:     budget.ContentBudgetTokens,
		InputLimitTokens:      budget.InputLimitTokens,
		OutputReserveTokens:   budget.OutputReserveTokens,
		PromptOverheadTokens:  budget.PromptOverheadTokens,
		SafetyMarginTokens:    budget.SafetyMarginTokens,
		CalibrationFactor:     budget.CalibrationFactor,
		TrimmedLayers:         trimmed,
		ModelName:             plan.Profile.ModelName,
		ContextWindowTokens:   plan.Profile.ContextWindowTokens,
	}

	// Convert the calibrated budget back to the local heuristic's units. This
	// ensures an underestimated prompt is trimmed before it reaches the model.
	rawBudget := int(math.Floor(float64(budget.ContentBudgetTokens) / budget.CalibrationFactor))
	if rawBudget <= 0 {
		return modelContext, fmt.Errorf("context_budget_exceeded: calibrated content budget is empty")
	}
	if estimateModelContextTokens(modelContext) <= rawBudget {
		return modelContext, nil
	}

	trimText := func(name string, value *string) bool {
		before := *value
		if mem.EstimateTextTokens(before) == 0 {
			return false
		}
		needed := estimateModelContextTokens(modelContext) - rawBudget
		keep := mem.EstimateTextTokens(before) - needed
		if keep < 0 {
			keep = 0
		}
		*value = trimTextToTokens(before, keep)
		if (name == "summary" || name == "session_state") && strings.HasPrefix(before, "Task ") {
			*value = mem.BoundContextSummary(before, keep, true)
		}
		if *value != before {
			trimmed = append(trimmed, name)
			return true
		}
		return false
	}

	for estimateModelContextTokens(modelContext) > rawBudget {
		before := estimateModelContextTokens(modelContext)
		progressed := false
		for _, item := range []struct {
			name  string
			value *string
		}{
			{"skills", &modelContext.Skills},
			{"project_context", &modelContext.ProjectContext},
			{"tool_results", &modelContext.ToolResults},
			{"durable_memory", &modelContext.DurableMemory},
			{"session_state", &modelContext.SessionState},
		} {
			if trimText(item.name, item.value) {
				progressed = true
			}
			if estimateModelContextTokens(modelContext) <= rawBudget {
				break
			}
		}
		if estimateModelContextTokens(modelContext) <= rawBudget {
			break
		}

		oldHistory := modelContext.History
		modelContext.History = budgetHistoryMessagesByTokens(modelContext.History, rawBudget/2)
		if len(modelContext.History) != len(oldHistory) || estimateMessagesContentTokens(modelContext.History) < estimateMessagesContentTokens(oldHistory) {
			trimmed = append(trimmed, "history")
			progressed = true
		}
		if estimateModelContextTokens(modelContext) <= rawBudget {
			break
		}

		if trimText("summary", &modelContext.Summary) {
			progressed = true
		}
		if !progressed || estimateModelContextTokens(modelContext) >= before {
			break
		}
	}

	modelContext.Budget.TrimmedLayers = uniqueStrings(trimmed)
	if estimateModelContextTokens(modelContext) > rawBudget {
		return modelContext, fmt.Errorf("context_budget_exceeded: context requires %d estimated tokens after safe trimming, available %d", estimateModelContextTokens(modelContext), rawBudget)
	}
	return modelContext, nil
}

func applySoftLayerBudgets(ctx context.Context, modelContext *ModelContext, plan contextBudgetPlan) []string {
	if modelContext == nil {
		return nil
	}
	limits := layerCharacterLimitsForPlan(ctx, plan)
	trimmed := make([]string, 0, 7)
	trimText := func(name string, value *string, limit int) {
		if value == nil || limit <= 0 {
			return
		}
		before := *value
		*value = trimRunesHead(*value, limit)
		if (name == "summary" || name == "session_state") && strings.HasPrefix(before, "Task ") {
			*value = mem.BoundContextSummary(before, limit, false)
		}
		if *value != before {
			trimmed = append(trimmed, name)
		}
	}
	trimText("summary", &modelContext.Summary, limits["summary"])
	trimText("session_state", &modelContext.SessionState, limits["session_state"])
	trimText("durable_memory", &modelContext.DurableMemory, limits["durable_memory"])
	trimText("tool_results", &modelContext.ToolResults, limits["tool_results"])
	trimText("project_context", &modelContext.ProjectContext, limits["project_context"])
	trimText("skills", &modelContext.Skills, limits["skills"])
	beforeHistory := estimateMessagesContentTokens(modelContext.History)
	modelContext.History = budgetHistoryMessages(modelContext.History, limits["history"])
	if estimateMessagesContentTokens(modelContext.History) < beforeHistory || len(modelContext.History) == 0 && beforeHistory > 0 {
		trimmed = append(trimmed, "history")
	}
	return uniqueStrings(trimmed)
}

func trimTextToTokens(value string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	if mem.EstimateTextTokens(value) <= maxTokens {
		return value
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high) / 2
		candidate := string(runes[mid:])
		if mem.EstimateTextTokens(candidate) <= maxTokens {
			high = mid
			continue
		}
		low = mid + 1
	}
	return string(runes[low:])
}

func estimateModelContextTokens(modelContext ModelContext) int {
	total := 0
	total += mem.EstimateTextTokens(modelContext.Content)
	total += mem.EstimateTextTokens(modelContext.Summary)
	total += mem.EstimateTextTokens(modelContext.SessionState)
	total += mem.EstimateTextTokens(modelContext.DurableMemory)
	total += mem.EstimateTextTokens(modelContext.ToolResults)
	total += mem.EstimateTextTokens(modelContext.ProjectContext)
	total += mem.EstimateTextTokens(modelContext.RequestAnalysis)
	total += mem.EstimateTextTokens(modelContext.CurrentTime)
	total += mem.EstimateTextTokens(modelContext.Skills)
	for _, message := range modelContext.History {
		total += mem.EstimateMessageTokens(message)
	}
	return total
}

func estimateMessagesContentTokens(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		if message != nil {
			total += mem.EstimateTextTokens(message.Content)
		}
	}
	return total
}

func budgetHistoryMessagesByTokens(history []*schema.Message, maxTokens int) []*schema.Message {
	if maxTokens <= 0 || len(history) == 0 {
		return []*schema.Message{}
	}
	start := len(history)
	total := 0
	for index := len(history) - 1; index >= 0; index-- {
		next := total + mem.EstimateMessageTokens(history[index])
		if next > maxTokens && start < len(history) {
			break
		}
		if next > maxTokens && start == len(history) {
			message := cloneHistory(history[index : index+1])
			if len(message) == 1 {
				message[0].Content = trimTextToTokens(message[0].Content, maxTokens-4)
			}
			return message
		}
		total = next
		start = index
	}
	start = alignHistoryPairStart(history, start)
	if start >= len(history) {
		return []*schema.Message{}
	}
	return cloneHistory(history[start:])
}

func alignHistoryPairStart(messages []*schema.Message, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	if isUserMessage(messages[start]) {
		return start
	}
	if isUserMessage(messages[start-1]) {
		return start - 1
	}
	for start < len(messages) && !isUserMessage(messages[start]) {
		start++
	}
	return start
}

func isUserMessage(msg *schema.Message) bool {
	return msg != nil && string(msg.Role) == "user"
}

func dynamicLayerChars(ctx context.Context, share float64, fallback int) int {
	return dynamicLayerCharsForPlan(ctx, defaultContextBudgetPlan(ctx), share, fallback)
}

func dynamicLayerCharsForPlan(ctx context.Context, plan contextBudgetPlan, share float64, fallback int) int {
	budget, err := plan.contentBudget(ctx, 0)
	if err != nil {
		return fallback
	}
	tokens := int(float64(budget.ContentBudgetTokens) * share)
	if tokens < minimumLayerToken {
		tokens = minimumLayerToken
	}
	return tokens * 4
}

// DynamicContextPolicy lets the controller fetch more or less history based
// on the configured model instead of imposing the old 3000-token ceiling.
func DynamicContextPolicy(ctx context.Context) mem.ContextPolicy {
	plan := defaultContextBudgetPlan(ctx)
	budget, err := plan.contentBudget(ctx, 0)
	if err != nil {
		return mem.DefaultContextPolicy()
	}
	historyTokens := int(float64(budget.ContentBudgetTokens) * historyShare)
	if historyTokens < minimumLayerToken {
		historyTokens = minimumLayerToken
	}
	return mem.ContextPolicy{
		MaxMessages: 20,
		MaxChars:    historyTokens * 4,
		MaxTokens:   historyTokens,
	}
}

func layerCharacterLimits(ctx context.Context) map[string]int {
	return layerCharacterLimitsForPlan(ctx, defaultContextBudgetPlan(ctx))
}

func layerCharacterLimitsForPlan(ctx context.Context, plan contextBudgetPlan) map[string]int {
	return map[string]int{
		"history":         dynamicLayerCharsForPlan(ctx, plan, historyShare, HistoryMaxChars),
		"summary":         dynamicLayerCharsForPlan(ctx, plan, summaryShare, SummaryMaxChars),
		"session_state":   dynamicLayerCharsForPlan(ctx, plan, sessionShare, SessionStateMaxChars),
		"durable_memory":  dynamicLayerCharsForPlan(ctx, plan, durableShare, DurableMemoryMaxChars),
		"tool_results":    dynamicLayerCharsForPlan(ctx, plan, toolResultsShare, ToolResultsMaxChars),
		"project_context": dynamicLayerCharsForPlan(ctx, plan, projectShare, 12000),
		"skills":          dynamicLayerCharsForPlan(ctx, plan, skillsShare, 6000),
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
