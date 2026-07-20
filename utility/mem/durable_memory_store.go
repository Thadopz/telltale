package mem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	durableMemoryStatusActive  = "active"
	durableMemoryStatusDeleted = "deleted"
	vectorStatusPending        = "pending"
	vectorStatusReady          = "ready"
)

type DurableMemory struct {
	ID                   int64     `json:"id"`
	UserID               string    `json:"user_id"`
	MemoryKey            string    `json:"memory_key"`
	Kind                 string    `json:"kind"`
	Content              string    `json:"content"`
	Confidence           float64   `json:"confidence"`
	Status               string    `json:"status"`
	VectorStatus         string    `json:"vector_status"`
	SourceConversationID string    `json:"source_conversation_id"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type DurableMemoryCandidate struct {
	ID                   int64     `json:"id"`
	UserID               string    `json:"user_id"`
	MemoryKey            string    `json:"memory_key"`
	Kind                 string    `json:"kind"`
	Content              string    `json:"content"`
	Confidence           float64   `json:"confidence"`
	SourceConversationID string    `json:"source_conversation_id"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type durableMemoryDB interface {
	Model(tableNameOrStruct ...interface{}) *gdb.Model
}

func ListDurableMemories(ctx context.Context, userID string) ([]DurableMemory, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user id is required")
	}

	var memories []DurableMemory
	err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Where("user_id", userID).
		Where("status", durableMemoryStatusActive).
		OrderDesc("updated_at").
		Scan(&memories)
	return memories, err
}

func ListDurableMemoryCandidates(ctx context.Context, userID string) ([]DurableMemoryCandidate, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user id is required")
	}

	var candidates []DurableMemoryCandidate
	err := g.DB().Model("durable_memory_candidates").
		Ctx(ctx).
		Where("user_id", userID).
		OrderDesc("updated_at").
		Scan(&candidates)
	return candidates, err
}

func DeleteDurableMemory(ctx context.Context, userID string, memoryID int64) error {
	memory, err := findDurableMemoryByID(ctx, userID, memoryID)
	if err != nil {
		return err
	}

	result, err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Where("id", memory.ID).
		Where("user_id", userID).
		Data(g.Map{
			"status":        durableMemoryStatusDeleted,
			"vector_status": vectorStatusPending,
		}).
		Update()
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return sql.ErrNoRows
	}

	if err = deleteDurableMemoryVector(ctx, memory.ID); err != nil {
		g.Log().Warningf(ctx, "delete durable memory vector failed memory_id=%d err=%v", memory.ID, err)
	}
	return nil
}

func ConfirmDurableMemoryCandidate(ctx context.Context, userID string, candidateID int64) (DurableMemory, error) {
	var saved DurableMemory
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		candidate, err := findCandidateWithDB(ctx, tx, userID, candidateID)
		if err != nil {
			return err
		}

		saved, err = upsertDurableMemoryWithDB(ctx, tx, userID, candidate.MemoryKey, candidate.Kind, candidate.Content, candidate.Confidence, candidate.SourceConversationID)
		if err != nil {
			return err
		}

		_, err = tx.Model("durable_memory_candidates").
			Ctx(ctx).
			Where("id", candidate.ID).
			Where("user_id", userID).
			Delete()
		return err
	})
	if err != nil {
		return DurableMemory{}, err
	}

	if err = syncDurableMemoryVector(ctx, saved); err != nil {
		g.Log().Warningf(ctx, "sync confirmed durable memory vector failed memory_id=%d err=%v", saved.ID, err)
	}
	return saved, nil
}

func RejectDurableMemoryCandidate(ctx context.Context, userID string, candidateID int64) error {
	result, err := g.DB().Model("durable_memory_candidates").
		Ctx(ctx).
		Where("id", candidateID).
		Where("user_id", strings.TrimSpace(userID)).
		Delete()
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func findDurableMemoryByKey(ctx context.Context, userID string, memoryKey string) (*DurableMemory, error) {
	var memory DurableMemory
	err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Where("user_id", strings.TrimSpace(userID)).
		Where("memory_key", strings.TrimSpace(memoryKey)).
		Where("status", durableMemoryStatusActive).
		Scan(&memory)
	if errors.Is(err, sql.ErrNoRows) || memory.ID == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &memory, nil
}

func findDurableMemoryByID(ctx context.Context, userID string, memoryID int64) (DurableMemory, error) {
	var memory DurableMemory
	err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Where("id", memoryID).
		Where("user_id", strings.TrimSpace(userID)).
		Where("status", durableMemoryStatusActive).
		Scan(&memory)
	if errors.Is(err, sql.ErrNoRows) || memory.ID == 0 {
		return DurableMemory{}, sql.ErrNoRows
	}
	return memory, err
}

func findCandidateWithDB(ctx context.Context, db durableMemoryDB, userID string, candidateID int64) (DurableMemoryCandidate, error) {
	var candidate DurableMemoryCandidate
	err := db.Model("durable_memory_candidates").
		Ctx(ctx).
		Where("id", candidateID).
		Where("user_id", strings.TrimSpace(userID)).
		Scan(&candidate)
	if errors.Is(err, sql.ErrNoRows) || candidate.ID == 0 {
		return DurableMemoryCandidate{}, sql.ErrNoRows
	}
	return candidate, err
}

func upsertDurableMemory(ctx context.Context, userID string, candidate MemoryCandidate, conversationID string) (DurableMemory, error) {
	var saved DurableMemory
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var err error
		saved, err = upsertDurableMemoryWithDB(ctx, tx, userID, candidate.MemoryKey, candidate.Kind, candidate.Content, candidate.Confidence, conversationID)
		return err
	})
	return saved, err
}

func upsertDurableMemoryWithDB(
	ctx context.Context,
	db durableMemoryDB,
	userID string,
	memoryKey string,
	kind string,
	content string,
	confidence float64,
	conversationID string,
) (DurableMemory, error) {
	data := g.Map{
		"user_id":                strings.TrimSpace(userID),
		"memory_key":             strings.TrimSpace(memoryKey),
		"kind":                   strings.TrimSpace(kind),
		"content":                strings.TrimSpace(content),
		"confidence":             confidence,
		"status":                 durableMemoryStatusActive,
		"vector_status":          vectorStatusPending,
		"source_conversation_id": strings.TrimSpace(conversationID),
	}

	if _, err := db.Model("durable_memories").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return DurableMemory{}, err
	}

	if _, err := db.Model("durable_memories").
		Ctx(ctx).
		Where("user_id", strings.TrimSpace(userID)).
		Where("memory_key", strings.TrimSpace(memoryKey)).
		Data(data).
		Update(); err != nil {
		return DurableMemory{}, err
	}

	var saved DurableMemory
	err := db.Model("durable_memories").
		Ctx(ctx).
		Where("user_id", strings.TrimSpace(userID)).
		Where("memory_key", strings.TrimSpace(memoryKey)).
		Scan(&saved)
	return saved, err
}

func upsertDurableMemoryCandidate(ctx context.Context, userID string, candidate MemoryCandidate, conversationID string) error {
	data := g.Map{
		"user_id":                strings.TrimSpace(userID),
		"memory_key":             candidate.MemoryKey,
		"kind":                   candidate.Kind,
		"content":                candidate.Content,
		"confidence":             candidate.Confidence,
		"source_conversation_id": strings.TrimSpace(conversationID),
	}

	if _, err := g.DB().Model("durable_memory_candidates").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return err
	}

	_, err := g.DB().Model("durable_memory_candidates").
		Ctx(ctx).
		Where("user_id", strings.TrimSpace(userID)).
		Where("memory_key", candidate.MemoryKey).
		Data(data).
		Update()
	return err
}

func markVectorReady(ctx context.Context, memoryID int64) error {
	_, err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Where("id", memoryID).
		Data(g.Map{"vector_status": vectorStatusReady}).
		Update()
	return err
}
