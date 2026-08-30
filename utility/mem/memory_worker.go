package mem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

type MemoryWorker struct {
	now func() time.Time
}

func NewMemoryWorker() *MemoryWorker {
	return &MemoryWorker{now: time.Now}
}

func (worker *MemoryWorker) Run(ctx context.Context) error {
	if worker == nil {
		return fmt.Errorf("memory worker is unavailable")
	}
	config := ConfiguredMemory(ctx)
	if config.Mode == MemoryModeOff {
		return nil
	}
	ticker := time.NewTicker(config.WorkerPollInterval)
	defer ticker.Stop()
	for {
		if _, err := worker.RunReady(ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.Log().Warningf(ctx, "memory worker cycle failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (worker *MemoryWorker) RunReady(ctx context.Context) (int, error) {
	if worker == nil {
		return 0, fmt.Errorf("memory worker is unavailable")
	}
	if ConfiguredMemory(ctx).Mode == MemoryModeOff {
		return 0, nil
	}
	processed := 0
	for {
		buffer, err := claimReadyMemoryBuffer(ctx, worker.now().UTC(), "")
		if err != nil {
			return processed, err
		}
		if buffer == nil {
			return processed, nil
		}
		processed++
		if err := worker.process(ctx, *buffer); err != nil {
			g.Log().Warningf(ctx, "memory buffer processing failed buffer=%s attempt=%d err=%v", buffer.ID, buffer.AttemptCount, err)
		}
	}
}

func (worker *MemoryWorker) FlushConversation(ctx context.Context, principal MemoryPrincipal, conversationID string) error {
	if worker == nil {
		return fmt.Errorf("memory worker is unavailable")
	}
	if err := principal.Validate(); err != nil {
		return err
	}
	if ConfiguredMemory(ctx).Mode == MemoryModeOff {
		return fmt.Errorf("memory is disabled")
	}
	bufferID := memoryScopeID(principal, conversationID)
	now := worker.now().UTC()
	result, err := g.DB().Model("memory_buffers").Ctx(ctx).
		Where("id", bufferID).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		WhereNot("status", memoryBufferRunning).
		WhereNot("pending_turn_ids", "[]").
		Data(g.Map{"explicit_flush": true, "status": memoryBufferPending, "next_attempt_at": now, "updated_at": now}).
		Update()
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("memory buffer has no pending turns or is already processing")
	}
	buffer, err := claimReadyMemoryBuffer(ctx, now, bufferID)
	if err != nil {
		return err
	}
	if buffer == nil {
		return fmt.Errorf("memory buffer is not ready")
	}
	return worker.process(ctx, *buffer)
}

func (worker *MemoryWorker) process(ctx context.Context, buffer memoryBuffer) (runErr error) {
	startedAt := worker.now()
	profileCount := 0
	eventCount := 0
	defer func() {
		delay := startedAt.Sub(buffer.LastActivityAt)
		if delay < 0 {
			delay = 0
		}
		recordMemoryFlush(ctx, ConfiguredMemory(ctx).Mode, worker.now().Sub(startedAt), delay, profileCount, eventCount, runErr)
	}()
	turnIDs := decodeTurnIDs(buffer.ClaimedTurnIDs)
	turns, err := loadTurnsByID(ctx, buffer.ConversationStorageID, turnIDs)
	if err != nil {
		return worker.recordFailure(ctx, buffer, err)
	}
	if len(turns) != len(turnIDs) {
		return worker.recordFailure(ctx, buffer, fmt.Errorf("one or more buffered turns are no longer available"))
	}

	config := ConfiguredMemory(ctx)
	extractionContext, cancel := context.WithTimeout(ctx, config.ExtractionTimeout)
	extraction, err := extractProfileAndEvents(extractionContext, turns)
	cancel()
	if err != nil {
		return worker.recordFailure(ctx, buffer, err)
	}
	profiles, events := validateMemoryExtraction(extraction, turns)
	sortValidatedMemory(profiles, events)
	profileCount = len(profiles)
	eventCount = len(events)
	eventIDs, err := commitMemoryExtraction(ctx, buffer, turns, profiles, events, worker.now().UTC())
	if err != nil {
		return worker.recordFailure(ctx, buffer, err)
	}
	syncMemoryEventVectors(ctx, eventIDs)
	return nil
}

func (worker *MemoryWorker) recordFailure(ctx context.Context, buffer memoryBuffer, runErr error) error {
	if err := failMemoryBuffer(ctx, buffer, runErr, worker.now().UTC()); err != nil {
		return err
	}
	return runErr
}

func HasExplicitMemoryIntent(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range []string{
		"记住这个", "请记住", "帮我记住", "记一下", "记住：", "记住:",
		"remember this", "please remember", "remember that", "save this to memory",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}
