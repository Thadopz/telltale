package mem

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gogf/gf/v2/frame/g"
)

type scoredMemoryEvent struct {
	Event MemoryEvent
	Score float64
}

func loadProfileEventContext(ctx context.Context, principal MemoryPrincipal, query string) (string, error) {
	startedAt := time.Now()
	if err := principal.Validate(); err != nil {
		recordMemoryContext(ctx, time.Since(startedAt), 0, 0, err)
		return "", err
	}
	config := ConfiguredMemory(ctx)
	if config.Mode != MemoryModeActive {
		return "", nil
	}

	profiles, err := relevantProfiles(ctx, principal, query)
	if err != nil {
		recordMemoryContext(ctx, time.Since(startedAt), 0, 0, err)
		return "", err
	}
	events, err := relevantEvents(ctx, principal, query, config)
	if err != nil {
		recordMemoryContext(ctx, time.Since(startedAt), len(profiles), 0, err)
		return "", err
	}
	recordMemoryContext(ctx, time.Since(startedAt), len(profiles), len(events), nil)
	return formatProfileEventContext(profiles, events, config), nil
}

func relevantProfiles(ctx context.Context, principal MemoryPrincipal, query string) ([]ProfileSlot, error) {
	var profiles []ProfileSlot
	err := g.DB().Model("profile_slots").Ctx(ctx).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		Where("status", profileStatusActive).
		Where("valid_until IS NULL OR valid_until > ?", time.Now().UTC()).
		OrderDesc("updated_at").
		Limit(50).
		Scan(&profiles)
	if err != nil {
		return nil, err
	}
	queryTerms := memoryTerms(query)
	filtered := make([]ProfileSlot, 0, len(profiles))
	for _, profile := range profiles {
		priority := profileContextPriority(profile, queryTerms)
		if priority <= 2 {
			filtered = append(filtered, profile)
		}
	}
	profiles = filtered
	sort.SliceStable(profiles, func(left, right int) bool {
		leftPriority := profileContextPriority(profiles[left], queryTerms)
		rightPriority := profileContextPriority(profiles[right], queryTerms)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return profiles[left].UpdatedAt.After(profiles[right].UpdatedAt)
	})
	if len(profiles) > 12 {
		profiles = profiles[:12]
	}
	return profiles, nil
}

func profileContextPriority(profile ProfileSlot, queryTerms map[string]bool) int {
	if strings.HasPrefix(profile.SlotKey, "constraint.") {
		return 0
	}
	if textMatchesTerms(profile.SlotKey+" "+profile.ValueText, queryTerms) {
		return 1
	}
	if strings.HasPrefix(profile.SlotKey, "preference.") {
		return 2
	}
	return 3
}

func relevantEvents(ctx context.Context, principal MemoryPrincipal, query string, config MemoryConfig) ([]MemoryEvent, error) {
	now := time.Now().UTC()
	var candidates []MemoryEvent
	model := g.DB().Model("memory_events").Ctx(ctx).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		Where("status", profileStatusActive).
		WhereGT("expires_at", now)
	if !queryRequestsHistoricalEvents(query) {
		model = model.WhereGTE("recorded_at", now.Add(-config.EventActiveAge))
	}
	err := model.
		OrderDesc("recorded_at").
		Limit(200).
		Scan(&candidates)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}

	queryTerms := memoryTerms(query)
	queryVector, vectorAvailable := memoryQueryVector(ctx, query)
	scored := make([]scoredMemoryEvent, 0, len(candidates))
	for _, event := range candidates {
		keyword := memoryTermScore(event.Summary+" "+event.TagsJSON, queryTerms)
		entity := memoryTermScore(event.EntitiesJSON, queryTerms)
		age := now.Sub(event.RecordedAt)
		timeScore := math.Exp(-age.Hours() / (24 * 30))
		vectorScore := 0.0
		if vectorAvailable && event.VectorStatus == "ready" {
			var eventVector []float32
			if json.Unmarshal([]byte(event.Vector), &eventVector) == nil {
				if similarity, ok := cosineSimilarity(queryVector, eventVector); ok {
					vectorScore = math.Max(0, similarity)
				}
			}
		}
		score := 0.25*keyword + 0.20*timeScore + 0.05*entity
		if vectorAvailable && event.VectorStatus == "ready" {
			score += 0.50 * vectorScore
		} else {
			score = score / 0.50
		}
		if score < 0.10 {
			continue
		}
		scored = append(scored, scoredMemoryEvent{Event: event, Score: score})
	}
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].Score != scored[right].Score {
			return scored[left].Score > scored[right].Score
		}
		return scored[left].Event.RecordedAt.After(scored[right].Event.RecordedAt)
	})

	result := make([]MemoryEvent, 0, 8)
	entityCounts := make(map[string]int)
	for _, item := range scored {
		entityKey := primaryEventEntity(item.Event)
		if entityKey != "" && entityCounts[entityKey] >= 3 {
			continue
		}
		result = append(result, item.Event)
		if entityKey != "" {
			entityCounts[entityKey]++
		}
		if len(result) >= 8 {
			break
		}
	}
	return result, nil
}

func queryRequestsHistoricalEvents(query string) bool {
	lower := strings.ToLower(query)
	markers := []string{
		"以前", "过去", "历史", "之前", "上次", "去年", "前年", "当时",
		"before", "previous", "history", "historical", "last year", "in the past",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func memoryQueryVector(ctx context.Context, query string) ([]float32, bool) {
	vector, err := embedMemoryContent(ctx, query)
	return vector, err == nil && len(vector) > 0
}

func formatProfileEventContext(profiles []ProfileSlot, events []MemoryEvent, config MemoryConfig) string {
	const totalMaxChars = 8000
	profileMax := int(float64(totalMaxChars) * config.ProfileRatio)
	eventMax := totalMaxChars - profileMax
	var profileBuilder strings.Builder
	for _, profile := range profiles {
		line := fmt.Sprintf("- slot=%s value=%s confirmed=%s updated_at=%s\n",
			profile.SlotKey,
			boundedMemoryText(profile.ValueText, 300),
			profile.Confirmation,
			profile.UpdatedAt.Format(time.RFC3339),
		)
		if profileBuilder.Len()+len(line) > profileMax {
			break
		}
		profileBuilder.WriteString(line)
	}
	var eventBuilder strings.Builder
	for _, event := range events {
		line := fmt.Sprintf("- event_id=%s recorded_at=%s evidence=%s historical_only=true summary=%s\n",
			event.ID,
			event.RecordedAt.Format(time.RFC3339),
			event.EvidenceStatus,
			boundedMemoryText(event.Summary, 500),
		)
		if eventBuilder.Len()+len(line) > eventMax {
			break
		}
		eventBuilder.WriteString(line)
	}
	parts := make([]string, 0, 2)
	if profileBuilder.Len() > 0 {
		parts = append(parts, "## Relevant user profile\n"+strings.TrimSpace(profileBuilder.String()))
	}
	if eventBuilder.Len() > 0 {
		parts = append(parts, "## Relevant past events\n"+strings.TrimSpace(eventBuilder.String()))
	}
	return strings.Join(parts, "\n\n")
}

func memoryTerms(text string) map[string]bool {
	terms := make(map[string]bool)
	var builder strings.Builder
	flush := func() {
		term := strings.ToLower(strings.TrimSpace(builder.String()))
		builder.Reset()
		if len([]rune(term)) >= 2 {
			terms[term] = true
		}
	}
	for _, character := range text {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' || character == '-' {
			builder.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return terms
}

func textMatchesTerms(text string, terms map[string]bool) bool {
	return memoryTermScore(text, terms) > 0
}

func memoryTermScore(text string, queryTerms map[string]bool) float64 {
	if len(queryTerms) == 0 {
		return 0
	}
	textTerms := memoryTerms(text)
	matches := 0
	for term := range queryTerms {
		if textTerms[term] || strings.Contains(strings.ToLower(text), term) {
			matches++
		}
	}
	return float64(matches) / float64(len(queryTerms))
}

func primaryEventEntity(event MemoryEvent) string {
	var entities []string
	if json.Unmarshal([]byte(event.EntitiesJSON), &entities) == nil && len(entities) > 0 {
		return strings.ToLower(entities[0])
	}
	return ""
}
