package mem

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

// ContextFact keeps a detail that must survive a context compaction.
// The natural-language summary can change, but these exact values stay
// available for later turns and for evidence lookups.
type ContextFact struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	SourceRole string `json:"source_role,omitempty"`
}

// CompactionPolicy controls when old messages are moved into the summary.
type CompactionPolicy struct {
	MaxTokens         int
	TriggerRatio      float64
	TargetRatio       float64
	MinRecentMessages int
	MaxSummaryChars   int
	MaxFacts          int
}

// CompactionMetadata is persisted with each successful compaction snapshot.
type CompactionMetadata struct {
	Generation        int       `json:"generation"`
	BeforeTokens      int       `json:"before_tokens"`
	AfterTokens       int       `json:"after_tokens"`
	CompactedMessages int       `json:"compacted_messages"`
	CreatedAt         time.Time `json:"created_at"`
	Pending           bool      `json:"pending"`
}

const (
	defaultCompactionMaxTokens         = 32768
	defaultDynamicCompactionMinTokens  = 8192
	defaultDynamicCompactionMaxTokens  = 65536
	defaultDynamicCompactionRatio      = 0.50
	defaultCompactionTriggerRatio      = 0.70
	defaultCompactionTargetRatio       = 0.55
	defaultCompactionMinRecentMessages = 4
	defaultCompactionMaxSummaryChars   = defaultSummaryMaxChars
	defaultCompactionMaxFacts          = 256
	defaultContextSummaryMaxChars      = 6000
)

const ContextSummaryMaxChars = defaultContextSummaryMaxChars

var (
	evidenceFactPattern   = regexp.MustCompile(`\b(?:ev|fq)_[A-Za-z0-9_-]+\b`)
	statusFactPattern     = regexp.MustCompile(`\b(?:HTTP\s*)?[1-5][0-9]{2}\b`)
	pathFactPattern       = regexp.MustCompile(`(?:https?://|/api/|/v[0-9]+/)[^\s\],;)]+`)
	keyValueFactPattern   = regexp.MustCompile(`(?i)\b(?:request[_-]?id|trace[_-]?id|order[_-]?id|error[_-]?code|sql[_-]?digest|max[_-]?open|max[_-]?idle|status|latency[_-]?ms)\s*[:=]\s*["'` + "`" + `]?([A-Za-z0-9._:/-]+)`)
	codeFactPattern       = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`)
	structuredFactPattern = regexp.MustCompile(`(?i)\b(claim_id|claim_type|assertion_kind|coverage_status|hypothesis_id|hypothesis_status|failed_query_id|evidence_id)\s*[:=]\s*["'` + "`" + `]?([A-Za-z0-9_.:/-]+)`)
)

// DefaultCompactionPolicy returns conservative defaults for a local agent.
func DefaultCompactionPolicy() CompactionPolicy {
	return CompactionPolicy{
		MaxTokens:         defaultCompactionMaxTokens,
		TriggerRatio:      defaultCompactionTriggerRatio,
		TargetRatio:       defaultCompactionTargetRatio,
		MinRecentMessages: defaultCompactionMinRecentMessages,
		MaxSummaryChars:   defaultCompactionMaxSummaryChars,
		MaxFacts:          defaultCompactionMaxFacts,
	}
}

// ConfiguredCompactionPolicy reads optional settings without making a
// missing configuration file a runtime error.
func ConfiguredCompactionPolicy(ctx context.Context) CompactionPolicy {
	policy := DefaultCompactionPolicy()
	explicitMaxTokens := false
	if value, err := g.Cfg().Get(ctx, "context.compaction.max_tokens"); err == nil && value != nil && value.Int() > 0 {
		policy.MaxTokens = value.Int()
		explicitMaxTokens = true
	}
	if !explicitMaxTokens {
		policy.MaxTokens = DynamicCompactionMaxTokens(ctx)
	}
	if value, err := g.Cfg().Get(ctx, "context.compaction.trigger_ratio"); err == nil && value != nil && value.Float64() > 0 {
		policy.TriggerRatio = value.Float64()
	}
	if value, err := g.Cfg().Get(ctx, "context.compaction.target_ratio"); err == nil && value != nil && value.Float64() > 0 {
		policy.TargetRatio = value.Float64()
	}
	if value, err := g.Cfg().Get(ctx, "context.compaction.min_recent_messages"); err == nil && value != nil && value.Int() > 0 {
		policy.MinRecentMessages = value.Int()
	}
	if value, err := g.Cfg().Get(ctx, "context.compaction.max_summary_chars"); err == nil && value != nil && value.Int() > 0 {
		policy.MaxSummaryChars = value.Int()
	}
	if value, err := g.Cfg().Get(ctx, "context.compaction.max_facts"); err == nil && value != nil && value.Int() > 0 {
		policy.MaxFacts = value.Int()
	}
	return normalizeCompactionPolicy(policy)
}

// DynamicCompactionMaxTokens derives the stored conversation threshold from
// the configured model's usable content budget. It intentionally leaves the
// prompt-specific system/tool overhead to the chat pipeline and remains
// conservative when the model configuration is incomplete.
func DynamicCompactionMaxTokens(ctx context.Context) int {
	profile := contextbudget.ConfiguredProfile(ctx, "quick_chat_model")
	if value, err := g.Cfg().Get(ctx, "agent.failover.enabled"); err == nil && value != nil && value.Bool() {
		backup := contextbudget.ConfiguredProfile(ctx, "failover_chat_model")
		profile = contextbudget.MergeProfiles(profile, &backup)
	}
	budget, err := contextbudget.Calculate(profile, 0, 0, 1)
	if err != nil {
		return defaultCompactionMaxTokens
	}
	limit := int(float64(budget.ContentBudgetTokens) * defaultDynamicCompactionRatio)
	if limit < defaultDynamicCompactionMinTokens {
		limit = defaultDynamicCompactionMinTokens
	}
	if limit > defaultDynamicCompactionMaxTokens {
		limit = defaultDynamicCompactionMaxTokens
	}
	return limit
}

func normalizeCompactionPolicy(policy CompactionPolicy) CompactionPolicy {
	defaults := DefaultCompactionPolicy()
	if policy.MaxTokens <= 0 {
		policy.MaxTokens = defaults.MaxTokens
	}
	if policy.TriggerRatio <= 0 || policy.TriggerRatio >= 1 {
		policy.TriggerRatio = defaults.TriggerRatio
	}
	if policy.TargetRatio <= 0 || policy.TargetRatio >= policy.TriggerRatio {
		policy.TargetRatio = defaults.TargetRatio
	}
	if policy.MinRecentMessages < 2 {
		policy.MinRecentMessages = defaults.MinRecentMessages
	}
	if policy.MaxSummaryChars <= 0 {
		policy.MaxSummaryChars = defaults.MaxSummaryChars
	}
	if policy.MaxFacts <= 0 {
		policy.MaxFacts = defaults.MaxFacts
	}
	return policy
}

// EstimateTextTokens is intentionally conservative. The project does not
// require a provider-specific tokenizer for compaction decisions: ASCII text
// is estimated at four characters per token and non-ASCII runes at one token.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}

	asciiCount := 0
	nonASCIICount := 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			asciiCount++
			continue
		}
		nonASCIICount++
	}
	return (asciiCount+3)/4 + nonASCIICount
}

// EstimateMessageTokens includes a small per-message role overhead.
func EstimateMessageTokens(message *schema.Message) int {
	if message == nil {
		return 0
	}
	return EstimateTextTokens(message.Content) + 4
}

// EstimateMessagesTokens estimates the size of a message list.
func EstimateMessagesTokens(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		total += EstimateMessageTokens(message)
	}
	return total
}

func (c *SimpleMemory) compactionPolicy() CompactionPolicy {
	if c.Policy.MaxTokens > 0 {
		return normalizeCompactionPolicy(c.Policy)
	}
	return DefaultCompactionPolicy()
}

// SetCompactionPolicy applies the current request's storage budget. A memory
// instance can survive model configuration changes, so the policy is updated
// at turn preparation rather than only at process startup.
func (c *SimpleMemory) SetCompactionPolicy(policy CompactionPolicy) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Policy = normalizeCompactionPolicy(policy)
	c.compactLocked()
	if c.compactionPending {
		// A policy change can compact already-loaded messages before the next
		// user turn is appended. Persist that snapshot immediately so token
		// counts, generation, and exact facts are not lost on a failed turn.
		c.persistLocked(nil)
	}
}

func (c *SimpleMemory) compactLocked() {
	if c == nil || len(c.Messages) == 0 {
		return
	}

	policy := c.compactionPolicy()
	maxStoredMessages := normalizeMaxMessages(c.MaxWindowSize)
	triggerTokens := int(float64(policy.MaxTokens) * policy.TriggerRatio)
	targetTokens := int(float64(policy.MaxTokens) * policy.TargetRatio)
	beforeTokens := EstimateMessagesTokens(c.Messages) + EstimateTextTokens(c.Summary)
	if len(c.Messages) <= maxStoredMessages && beforeTokens <= triggerTokens {
		return
	}

	compactedMessages := 0
	for len(c.Messages) > maxStoredMessages || EstimateMessagesTokens(c.Messages)+EstimateTextTokens(c.Summary) > targetTokens {
		if len(c.Messages) <= 1 {
			break
		}

		minRecent := policy.MinRecentMessages
		if minRecent >= len(c.Messages) {
			minRecent = len(c.Messages) - 1
		}
		if minRecent < 1 {
			minRecent = 1
		}
		maxRemovable := len(c.Messages) - minRecent
		if maxRemovable <= 0 {
			break
		}

		removeCount := len(c.Messages) - maxStoredMessages
		if removeCount <= 0 {
			removeCount = 2
		}
		if removeCount > maxRemovable {
			removeCount = maxRemovable
		}
		if removeCount > 1 && removeCount%2 != 0 && removeCount+1 <= maxRemovable {
			removeCount++
		}

		c.mergeSummary(c.Messages[:removeCount])
		c.Messages = cloneMessages(c.Messages[removeCount:])
		compactedMessages += removeCount
	}

	if compactedMessages == 0 {
		return
	}

	c.Compaction.Generation++
	c.Compaction.BeforeTokens = beforeTokens
	c.Compaction.AfterTokens = EstimateMessagesTokens(c.Messages) + EstimateTextTokens(c.Summary)
	c.Compaction.CompactedMessages = compactedMessages
	c.Compaction.CreatedAt = time.Now()
	c.Compaction.Pending = true
	c.compactionPending = true
}

func (c *SimpleMemory) mergeSummary(messages []*schema.Message) {
	if len(messages) == 0 {
		return
	}

	c.Facts = mergeContextFacts(c.Facts, extractContextFacts(messages), c.compactionPolicy().MaxFacts)
	summaryPart := summarizeMessages(messages)
	if summaryPart == "" {
		return
	}
	if strings.TrimSpace(c.Summary) == "" {
		c.Summary = summaryPart
	} else {
		c.Summary += "\n" + summaryPart
	}
	c.Summary = trimRunes(c.Summary, c.compactionPolicy().MaxSummaryChars)
}

// GetContextSummary returns the narrative summary followed by exact details.
// Exact details are placed last so the existing tail-based prompt budget keeps
// them when it has to trim the string.
func (c *SimpleMemory) GetContextSummary(maxChars int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	parts := make([]string, 0, 2)
	if strings.TrimSpace(c.Summary) != "" {
		parts = append(parts, c.Summary)
	}
	facts := formatContextFacts(c.Facts)
	if facts != "" {
		parts = append(parts, "Critical exact details:\n"+facts)
	}
	result := strings.Join(parts, "\n\n")
	if maxChars <= 0 {
		maxChars = defaultContextSummaryMaxChars
	}
	return trimRunes(result, maxChars)
}

func (c *SimpleMemory) GetContextFacts() []ContextFact {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ContextFact(nil), c.Facts...)
}

// AddContextFacts stores machine-readable claim and evidence details without
// adding them to the user-visible conversation. These compact facts let later
// compaction preserve references even when narrative prose is trimmed.
func (c *SimpleMemory) AddContextFacts(facts []ContextFact) {
	if c == nil || len(facts) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Facts = mergeContextFacts(c.Facts, facts, c.compactionPolicy().MaxFacts)
	c.UpdatedAt = time.Now()
	if factStore, ok := c.store.(contextFactsStore); ok {
		snapshot := &memorySnapshot{
			ID:            c.ID,
			UserID:        c.UserID,
			Messages:      cloneMessages(c.Messages),
			Summary:       c.Summary,
			Facts:         append([]ContextFact(nil), c.Facts...),
			Compaction:    c.Compaction,
			MaxWindowSize: c.MaxWindowSize,
			UpdatedAt:     c.UpdatedAt,
		}
		snapshot.Compaction.Pending = c.compactionPending
		if err := factStore.SaveContextFacts(snapshot); err != nil {
			logMemoryPersistError(c, nil, err)
		} else {
			c.compactionPending = false
			c.Compaction.Pending = false
		}
		return
	}
	c.persistLocked(nil)
}

func extractContextFacts(messages []*schema.Message) []ContextFact {
	facts := make([]ContextFact, 0)
	for _, message := range messages {
		if message == nil || strings.TrimSpace(message.Content) == "" {
			continue
		}
		content := message.Content
		for _, match := range evidenceFactPattern.FindAllString(content, -1) {
			facts = append(facts, ContextFact{Kind: "evidence_id", Value: match, SourceRole: string(message.Role)})
		}
		for _, match := range structuredFactPattern.FindAllStringSubmatch(content, -1) {
			if len(match) < 3 {
				continue
			}
			facts = append(facts, ContextFact{Kind: strings.ToLower(strings.TrimSpace(match[1])), Value: strings.TrimSpace(match[2]), SourceRole: string(message.Role)})
		}
		for _, match := range statusFactPattern.FindAllString(content, -1) {
			facts = append(facts, ContextFact{Kind: "status_or_code", Value: strings.TrimSpace(match), SourceRole: string(message.Role)})
		}
		for _, match := range pathFactPattern.FindAllString(content, -1) {
			facts = append(facts, ContextFact{Kind: "path_or_url", Value: strings.TrimRight(match, "."), SourceRole: string(message.Role)})
		}
		for _, match := range keyValueFactPattern.FindAllStringSubmatch(content, -1) {
			if len(match) < 2 {
				continue
			}
			facts = append(facts, ContextFact{Kind: "key_value", Value: strings.TrimSpace(match[0]), SourceRole: string(message.Role)})
		}
		for _, match := range codeFactPattern.FindAllString(content, -1) {
			if match == "HTTP" || match == "JSON" || match == "SQL" {
				continue
			}
			facts = append(facts, ContextFact{Kind: "error_or_symbol", Value: match, SourceRole: string(message.Role)})
		}
	}
	return facts
}

func mergeContextFacts(existing []ContextFact, incoming []ContextFact, maxFacts int) []ContextFact {
	merged := make([]ContextFact, 0, len(existing)+len(incoming))
	seen := make(map[string]bool)
	appendFact := func(fact ContextFact) {
		fact.Kind = strings.TrimSpace(fact.Kind)
		fact.Value = strings.TrimSpace(fact.Value)
		if fact.Kind == "" || fact.Value == "" {
			return
		}
		key := fact.Kind + "\x00" + fact.Value
		if seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, fact)
	}
	for _, fact := range existing {
		appendFact(fact)
	}
	for _, fact := range incoming {
		appendFact(fact)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return contextFactPriority(merged[i].Kind) < contextFactPriority(merged[j].Kind)
	})
	if maxFacts > 0 && len(merged) > maxFacts {
		merged = merged[:maxFacts]
	}
	return merged
}

func contextFactPriority(kind string) int {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "claim_id", "claim_type", "assertion_kind", "coverage_status", "hypothesis_id", "hypothesis_status", "evidence_id", "failed_query_id":
		return 0
	case "key_value", "path_or_url", "status_or_code":
		return 1
	default:
		return 2
	}
}

func formatContextFacts(facts []ContextFact) string {
	if len(facts) == 0 {
		return ""
	}
	lines := make([]string, 0, len(facts))
	for _, fact := range facts {
		if strings.TrimSpace(fact.Kind) == "" || strings.TrimSpace(fact.Value) == "" {
			continue
		}
		lines = append(lines, "- "+fact.Kind+": "+fact.Value)
	}
	return strings.Join(lines, "\n")
}

func budgetMessagesByTokens(messages []*schema.Message, maxTokens int) []*schema.Message {
	if maxTokens <= 0 || len(messages) == 0 {
		return cloneMessages(messages)
	}

	start := len(messages)
	total := 0
	for index := len(messages) - 1; index >= 0; index-- {
		nextTotal := total + EstimateMessageTokens(messages[index])
		if nextTotal > maxTokens && start < len(messages) {
			break
		}
		total = nextTotal
		start = index
	}
	start = alignPairStart(start, len(messages))
	if start >= len(messages) {
		return []*schema.Message{}
	}
	return cloneMessages(messages[start:])
}
