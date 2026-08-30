package mem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

type memoryTurn struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

// EnqueueCompletedTurn records the two turns that were just persisted by the
// diagnostic service. The buffer stores turn IDs, not another copy of the
// conversation text.
func EnqueueCompletedTurn(
	ctx context.Context,
	principal MemoryPrincipal,
	conversationID string,
	conversationStorageID string,
	explicitFlush bool,
) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(conversationStorageID) == "" {
		return fmt.Errorf("memory conversation scope is required")
	}
	if ConfiguredMemory(ctx).Mode == MemoryModeOff {
		return nil
	}

	turns, err := recentConversationTurns(ctx, conversationStorageID, 2)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return nil
	}
	turnIDs := make([]int64, 0, len(turns))
	for _, turn := range turns {
		turnIDs = append(turnIDs, turn.ID)
	}

	now := time.Now().UTC()
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var existing memoryBuffer
		err := tx.Model("memory_buffers").Ctx(ctx).
			Where("tenant_id", principal.TenantID).
			Where("user_id", principal.UserID).
			Where("conversation_id", conversationID).
			Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load memory buffer: %w", err)
		}

		pendingIDs := decodeTurnIDs(existing.PendingTurnIDs)
		pendingIDs = appendUniqueTurnIDs(pendingIDs, turnIDs...)
		pendingIDs = excludeTurnIDs(pendingIDs, decodeTurnIDs(existing.ClaimedTurnIDs))
		pendingTokens := estimateTurnsByID(ctx, conversationStorageID, pendingIDs)
		if existing.ID == "" {
			_, err = tx.Model("memory_buffers").Ctx(ctx).Data(g.Map{
				"id":                      memoryScopeID(principal, conversationID),
				"tenant_id":               principal.TenantID,
				"user_id":                 principal.UserID,
				"conversation_id":         conversationID,
				"conversation_storage_id": conversationStorageID,
				"pending_turn_ids":        marshalMemoryJSON(pendingIDs),
				"pending_tokens":          pendingTokens,
				"status":                  memoryBufferPending,
				"explicit_flush":          explicitFlush,
				"next_attempt_at":         now,
				"last_activity_at":        now,
				"created_at":              now,
				"updated_at":              now,
			}).Insert()
			return err
		}

		status := existing.Status
		if status == memoryBufferProcessed || status == memoryBufferDeadLetter {
			status = memoryBufferPending
		}
		_, err = tx.Model("memory_buffers").Ctx(ctx).Where("id", existing.ID).Data(g.Map{
			"conversation_storage_id": conversationStorageID,
			"pending_turn_ids":        marshalMemoryJSON(pendingIDs),
			"pending_tokens":          pendingTokens,
			"status":                  status,
			"explicit_flush":          existing.ExplicitFlush || explicitFlush,
			"next_attempt_at":         now,
			"last_activity_at":        now,
			"last_error":              "",
			"updated_at":              now,
		}).Update()
		return err
	})
}

func recentConversationTurns(ctx context.Context, storageID string, limit int) ([]memoryTurn, error) {
	var descending []memoryTurn
	err := g.DB().Model("conversation_turns").Ctx(ctx).
		Fields("id", "role", "content").
		Where("conversation_id", storageID).
		OrderDesc("id").
		Limit(limit).
		Scan(&descending)
	if err != nil {
		return nil, fmt.Errorf("load recent conversation turns: %w", err)
	}
	for left, right := 0, len(descending)-1; left < right; left, right = left+1, right-1 {
		descending[left], descending[right] = descending[right], descending[left]
	}
	return descending, nil
}

func loadTurnsByID(ctx context.Context, storageID string, turnIDs []int64) ([]memoryTurn, error) {
	if len(turnIDs) == 0 {
		return nil, nil
	}
	var turns []memoryTurn
	err := g.DB().Model("conversation_turns").Ctx(ctx).
		Fields("id", "role", "content").
		Where("conversation_id", storageID).
		WhereIn("id", turnIDs).
		OrderAsc("id").
		Scan(&turns)
	if err != nil {
		return nil, fmt.Errorf("load buffered conversation turns: %w", err)
	}
	return turns, nil
}

func claimReadyMemoryBuffer(ctx context.Context, now time.Time, onlyBufferID string) (*memoryBuffer, error) {
	config := ConfiguredMemory(ctx)
	var claimed *memoryBuffer
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Model("memory_buffers").Ctx(ctx).
			Where("status", memoryBufferRunning).
			WhereLTE("lease_until", now).
			WhereGTE("attempt_count", config.MaxAttempts).
			Data(g.Map{
				"status":      memoryBufferDeadLetter,
				"lease_until": nil,
				"last_error":  "memory buffer lease expired after maximum attempts",
				"updated_at":  now,
			}).Update(); err != nil {
			return err
		}

		model := tx.Model("memory_buffers").Ctx(ctx)
		if onlyBufferID != "" {
			model = model.Where("id", onlyBufferID)
		}
		readyBefore := now.Add(-config.IdleFlush)
		condition := "((status = ? AND next_attempt_at <= ? AND (pending_tokens >= ? OR explicit_flush = 1 OR last_activity_at <= ?)) OR (status = ? AND lease_until <= ? AND attempt_count < ?))"
		var row memoryBuffer
		err := model.Where(condition,
			memoryBufferPending, now, config.TriggerTokens, readyBefore,
			memoryBufferRunning, now, config.MaxAttempts,
		).OrderAsc("next_attempt_at").OrderAsc("last_activity_at").Limit(1).Scan(&row)
		if errors.Is(err, sql.ErrNoRows) || row.ID == "" {
			return nil
		}
		if err != nil {
			return err
		}

		pendingIDs := decodeTurnIDs(row.PendingTurnIDs)
		claimedIDs := decodeTurnIDs(row.ClaimedTurnIDs)
		remainingIDs := pendingIDs
		if row.Status != memoryBufferRunning || len(claimedIDs) == 0 {
			claimedIDs, remainingIDs = splitTurnIDsByBudget(ctx, row.ConversationStorageID, pendingIDs, config.MaxBatchTokens)
		}
		if len(claimedIDs) == 0 {
			return nil
		}
		claimedTokens := estimateTurnsByID(ctx, row.ConversationStorageID, claimedIDs)
		remainingTokens := estimateTurnsByID(ctx, row.ConversationStorageID, remainingIDs)
		leaseUntil := now.Add(config.LeaseDuration)
		result, err := tx.Model("memory_buffers").Ctx(ctx).
			Where("id", row.ID).
			Where(condition,
				memoryBufferPending, now, config.TriggerTokens, readyBefore,
				memoryBufferRunning, now, config.MaxAttempts,
			).
			Data(g.Map{
				"pending_turn_ids": marshalMemoryJSON(remainingIDs),
				"claimed_turn_ids": marshalMemoryJSON(claimedIDs),
				"pending_tokens":   remainingTokens,
				"claimed_tokens":   claimedTokens,
				"status":           memoryBufferRunning,
				"attempt_count":    row.AttemptCount + 1,
				"lease_until":      leaseUntil,
				"explicit_flush":   false,
				"updated_at":       now,
			}).Update()
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return nil
		}
		row.PendingTurnIDs = marshalMemoryJSON(remainingIDs)
		row.ClaimedTurnIDs = marshalMemoryJSON(claimedIDs)
		row.PendingTokens = remainingTokens
		row.ClaimedTokens = claimedTokens
		row.Status = memoryBufferRunning
		row.AttemptCount++
		row.LeaseUntil = &leaseUntil
		claimed = &row
		return nil
	})
	return claimed, err
}

func splitTurnIDsByBudget(ctx context.Context, storageID string, turnIDs []int64, maxTokens int) ([]int64, []int64) {
	turns, err := loadTurnsByID(ctx, storageID, turnIDs)
	if err != nil || len(turns) == 0 {
		return nil, turnIDs
	}
	claimed := make([]int64, 0, len(turns))
	total := 0
	for _, turn := range turns {
		tokens := contextbudget.EstimateTextTokens(turn.Content) + 4
		if len(claimed) > 0 && total+tokens > maxTokens {
			break
		}
		claimed = append(claimed, turn.ID)
		total += tokens
	}
	claimedSet := make(map[int64]bool, len(claimed))
	for _, id := range claimed {
		claimedSet[id] = true
	}
	remaining := make([]int64, 0, len(turnIDs)-len(claimed))
	for _, id := range turnIDs {
		if !claimedSet[id] {
			remaining = append(remaining, id)
		}
	}
	return claimed, remaining
}

func estimateTurnsByID(ctx context.Context, storageID string, turnIDs []int64) int {
	turns, err := loadTurnsByID(ctx, storageID, turnIDs)
	if err != nil {
		return 0
	}
	total := 0
	for _, turn := range turns {
		total += contextbudget.EstimateTextTokens(turn.Content) + 4
	}
	return total
}

func failMemoryBuffer(ctx context.Context, buffer memoryBuffer, runErr error, now time.Time) error {
	config := ConfiguredMemory(ctx)
	status := memoryBufferPending
	nextAttempt := now.Add(memoryRetryDelay(buffer.AttemptCount))
	if buffer.AttemptCount >= config.MaxAttempts {
		status = memoryBufferDeadLetter
	}
	_, err := g.DB().Model("memory_buffers").Ctx(ctx).
		Where("id", buffer.ID).
		Where("status", memoryBufferRunning).
		Data(g.Map{
			"status":          status,
			"lease_until":     nil,
			"next_attempt_at": nextAttempt,
			"last_error":      boundedMemoryText(redactText(runErr.Error()), 1000),
			"updated_at":      now,
		}).Update()
	return err
}

func memoryRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		return delays[len(delays)-1]
	}
	return delays[index]
}

func decodeTurnIDs(raw string) []int64 {
	var ids []int64
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil
	}
	return ids
}

func appendUniqueTurnIDs(existing []int64, incoming ...int64) []int64 {
	seen := make(map[int64]bool, len(existing)+len(incoming))
	result := make([]int64, 0, len(existing)+len(incoming))
	for _, id := range append(existing, incoming...) {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func excludeTurnIDs(ids []int64, excluded []int64) []int64 {
	excludedSet := make(map[int64]bool, len(excluded))
	for _, id := range excluded {
		excludedSet[id] = true
	}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !excludedSet[id] {
			result = append(result, id)
		}
	}
	return result
}
