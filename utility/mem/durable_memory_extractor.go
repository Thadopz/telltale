package mem

import (
	"SREagent/internal/ai/models"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	defaultMemoryExtractionTimeout = 8 * time.Second
	defaultMemoryMinConfidence     = 0.8
	defaultDuplicateSimilarity     = 0.90
	maxDurableMemoryChars          = 500
)

type MemoryCandidate struct {
	MemoryKey  string  `json:"memory_key"`
	Kind       string  `json:"kind"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
}

var allowedMemoryKeys = map[string]string{
	"preference.response_language": "preference",
	"preference.answer_detail":     "preference",
	"preference.output_format":     "preference",
	"profile.responsible_services": "profile",
	"environment.default_cluster":  "environment",
	"environment.technology_stack": "environment",
	"constraint.production_change": "constraint",
	"constraint.database_write":    "constraint",
}

var sensitiveMemoryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|passwd|secret|private[_ -]?key)\b\s*[:=]`),
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(bearer)\s+[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\b(mysql|postgres|mongodb|redis)://[^@\s]+@`),
}

var temporaryMemoryPhrases = []string{
	"当前错误率",
	"当前成功率",
	"当前延迟",
	"当前告警",
	"本次故障",
	"这次故障",
	"刚才查询",
	"工具返回",
	"today's error rate",
	"current error rate",
	"current latency",
	"current alert",
	"this incident",
	"tool returned",
}

func ExtractAndSaveDurableMemories(
	ctx context.Context,
	userID string,
	conversationID string,
	userQuestion string,
	assistantAnswer string,
) error {
	if !durableMemoryEnabled(ctx) {
		return nil
	}

	timeout := durableMemoryExtractionTimeout(ctx)
	extractionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	candidates, err := extractMemoryCandidates(extractionCtx, userQuestion, assistantAnswer)
	if err != nil {
		return fmt.Errorf("extract durable memory candidates: %w", err)
	}

	for _, candidate := range candidates {
		candidate, valid := validateMemoryCandidate(extractionCtx, candidate)
		if !valid {
			continue
		}
		if err = saveMemoryCandidate(extractionCtx, strings.TrimSpace(userID), strings.TrimSpace(conversationID), candidate); err != nil {
			return err
		}
	}
	return nil
}

func extractMemoryCandidates(ctx context.Context, userQuestion string, assistantAnswer string) ([]MemoryCandidate, error) {
	model, err := models.OpenAIForDeepSeekV3Quick(ctx)
	if err != nil {
		return nil, err
	}

	response, err := model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(memoryExtractionPrompt()),
		schema.UserMessage(
			"User message:\n" + strings.TrimSpace(userQuestion) +
				"\n\nAssistant answer:\n" + strings.TrimSpace(assistantAnswer),
		),
	})
	if err != nil {
		return nil, err
	}
	return parseMemoryCandidates(response.Content)
}

func memoryExtractionPrompt() string {
	return `Extract durable user memories from the conversation.
Return only a JSON array. Return [] when there is no durable memory.

Allowed memory keys and required kinds:
- preference.response_language -> preference
- preference.answer_detail -> preference
- preference.output_format -> preference
- profile.responsible_services -> profile
- environment.default_cluster -> environment
- environment.technology_stack -> environment
- constraint.production_change -> constraint
- constraint.database_write -> constraint

Each item must have this shape:
{"memory_key":"...","kind":"...","content":"...","confidence":0.0}

Rules:
- Save only stable information explicitly stated or confirmed by the user.
- Do not save temporary incident state, current metrics, alerts, tool output, or unverified assumptions.
- Do not save passwords, tokens, private keys, connection credentials, or other secrets.
- Do not invent memory keys.
- Write content as one short factual sentence.`
}

func parseMemoryCandidates(raw string) ([]MemoryCandidate, error) {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("model response does not contain a JSON array")
	}

	var candidates []MemoryCandidate
	if err := json.Unmarshal([]byte(raw[start:end+1]), &candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

func validateMemoryCandidate(ctx context.Context, candidate MemoryCandidate) (MemoryCandidate, bool) {
	candidate.MemoryKey = strings.TrimSpace(candidate.MemoryKey)
	candidate.Kind = strings.TrimSpace(candidate.Kind)
	candidate.Content = strings.TrimSpace(strings.ReplaceAll(candidate.Content, "\n", " "))

	expectedKind, allowed := allowedMemoryKeys[candidate.MemoryKey]
	if !allowed || candidate.Kind != expectedKind {
		return MemoryCandidate{}, false
	}
	if candidate.Confidence < durableMemoryMinConfidence(ctx) {
		return MemoryCandidate{}, false
	}
	if candidate.Content == "" || utf8.RuneCountInString(candidate.Content) > maxDurableMemoryChars {
		return MemoryCandidate{}, false
	}
	if containsSensitiveMemory(candidate.Content) || containsTemporaryMemory(candidate.Content) {
		return MemoryCandidate{}, false
	}
	return candidate, true
}

func containsSensitiveMemory(content string) bool {
	for _, pattern := range sensitiveMemoryPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}

func containsTemporaryMemory(content string) bool {
	normalized := strings.ToLower(content)
	for _, phrase := range temporaryMemoryPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func saveMemoryCandidate(ctx context.Context, userID string, conversationID string, candidate MemoryCandidate) error {
	if userID == "" {
		return fmt.Errorf("save durable memory: user id is required")
	}

	if !isLowRiskMemoryKey(candidate.MemoryKey) {
		return upsertDurableMemoryCandidate(ctx, userID, candidate, conversationID)
	}

	existing, err := findDurableMemoryByKey(ctx, userID, candidate.MemoryKey)
	if err != nil {
		return fmt.Errorf("find durable memory: %w", err)
	}
	if existing == nil {
		return saveActiveMemory(ctx, userID, conversationID, candidate)
	}

	similarity, available := durableMemorySimilarity(ctx, *existing, candidate.Content)
	if !available {
		return upsertDurableMemoryCandidate(ctx, userID, candidate, conversationID)
	}
	if similarity >= durableMemoryDuplicateSimilarity(ctx) {
		return nil
	}
	return saveActiveMemory(ctx, userID, conversationID, candidate)
}

func saveActiveMemory(ctx context.Context, userID string, conversationID string, candidate MemoryCandidate) error {
	saved, err := upsertDurableMemory(ctx, userID, candidate, conversationID)
	if err != nil {
		return fmt.Errorf("upsert durable memory: %w", err)
	}
	if err = syncDurableMemoryVector(ctx, saved); err != nil {
		g.Log().Warningf(ctx, "durable memory vector sync failed memory_id=%d err=%v", saved.ID, err)
	}
	return nil
}

func isLowRiskMemoryKey(memoryKey string) bool {
	return strings.HasPrefix(memoryKey, "preference.")
}

func durableMemoryEnabled(ctx context.Context) bool {
	value, err := g.Cfg().Get(ctx, "durable_memory.enabled")
	return err != nil || value == nil || value.Bool()
}

func durableMemoryExtractionTimeout(ctx context.Context) time.Duration {
	value, err := g.Cfg().Get(ctx, "durable_memory.extraction_timeout_seconds")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultMemoryExtractionTimeout
	}
	return time.Duration(value.Int()) * time.Second
}

func durableMemoryMinConfidence(ctx context.Context) float64 {
	value, err := g.Cfg().Get(ctx, "durable_memory.min_confidence")
	if err != nil || value == nil || value.Float64() <= 0 {
		return defaultMemoryMinConfidence
	}
	return value.Float64()
}

func durableMemoryDuplicateSimilarity(ctx context.Context) float64 {
	value, err := g.Cfg().Get(ctx, "durable_memory.duplicate_similarity")
	if err != nil || value == nil || value.Float64() <= 0 {
		return defaultDuplicateSimilarity
	}
	return value.Float64()
}
