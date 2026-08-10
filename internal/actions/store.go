package actions

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

func (s *memoryActionStore) Save(_ context.Context, action pendingAction) error {
	s.Lock()
	defer s.Unlock()
	s.pruneLocked(time.Now())
	s.actions[action.Token] = action
	return nil
}

func (s *memoryActionStore) Load(_ context.Context, token string) (pendingAction, error) {
	s.Lock()
	defer s.Unlock()
	s.pruneLocked(time.Now())
	action, ok := s.actions[token]
	if !ok {
		return pendingAction{}, sql.ErrNoRows
	}
	return action, nil
}

func (s *memoryActionStore) Confirm(_ context.Context, token string) error {
	s.Lock()
	defer s.Unlock()
	action, ok := s.actions[token]
	if !ok {
		return sql.ErrNoRows
	}
	action.Confirmed = true
	s.actions[token] = action
	return nil
}

func (s *memoryActionStore) ConsumeConfirmed(_ context.Context, token string, now time.Time) error {
	s.Lock()
	defer s.Unlock()
	s.pruneLocked(now)
	action, ok := s.actions[token]
	if !ok || !action.Confirmed {
		return sql.ErrNoRows
	}
	delete(s.actions, token)
	return nil
}

func (s *memoryActionStore) Delete(_ context.Context, token string) error {
	s.Lock()
	defer s.Unlock()
	delete(s.actions, token)
	return nil
}

func (s *memoryActionStore) Prune(_ context.Context, now time.Time) error {
	s.Lock()
	defer s.Unlock()
	s.pruneLocked(now)
	return nil
}

func (s *memoryActionStore) pruneLocked(now time.Time) {
	for token, action := range s.actions {
		if now.After(action.ExpiresAt) {
			delete(s.actions, token)
		}
	}
}

func (s *memoryActionStore) SaveOperation(_ context.Context, operation OperationRecord) error {
	s.Lock()
	defer s.Unlock()
	if s.operations == nil {
		s.operations = make(map[string]OperationRecord)
	}
	if _, exists := s.operations[operation.OperationID]; exists {
		return errors.New("operation_id already exists")
	}
	s.operations[operation.OperationID] = operation
	return nil
}

func (s *memoryActionStore) UpdateOperation(_ context.Context, operation OperationRecord) error {
	s.Lock()
	defer s.Unlock()
	if s.operations == nil {
		return sql.ErrNoRows
	}
	if _, exists := s.operations[operation.OperationID]; !exists {
		return sql.ErrNoRows
	}
	s.operations[operation.OperationID] = operation
	return nil
}

func (s *memoryActionStore) LoadOperation(_ context.Context, operationID string) (OperationRecord, error) {
	s.Lock()
	defer s.Unlock()
	operation, ok := s.operations[operationID]
	if !ok {
		return OperationRecord{}, sql.ErrNoRows
	}
	return operation, nil
}

type sqliteActionRow struct {
	Token           string    `json:"token"`
	ToolName        string    `json:"tool_name"`
	OperationID     string    `json:"operation_id"`
	ArgumentsHash   string    `json:"arguments_hash"`
	Risk            string    `json:"risk"`
	RequiresConfirm bool      `json:"requires_confirm"`
	ExpiresAt       time.Time `json:"expires_at"`
	UserID          string    `json:"user_id"`
	TenantID        string    `json:"tenant_id"`
	RolesKey        string    `json:"roles_key"`
	ArgumentsJSON   string    `json:"arguments_json"`
	Confirmed       bool      `json:"confirmed"`
}

type sqliteActionStore struct{}

type sqliteOperationRow struct {
	OperationID    string    `json:"operation_id"`
	ConversationID string    `json:"conversation_id"`
	UserID         string    `json:"user_id"`
	TenantID       string    `json:"tenant_id"`
	ToolName       string    `json:"tool_name"`
	Status         string    `json:"status"`
	Result         string    `json:"result"`
	EvidenceID     string    `json:"evidence_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *sqliteActionStore) Save(ctx context.Context, action pendingAction) error {
	data := g.Map{
		"token":            action.Token,
		"tool_name":        action.ToolName,
		"operation_id":     action.OperationID,
		"arguments_hash":   action.ArgumentsHash,
		"risk":             action.Risk,
		"requires_confirm": action.RequiresConfirm,
		"expires_at":       action.ExpiresAt,
		"user_id":          action.UserID,
		"tenant_id":        action.TenantID,
		"roles_key":        action.RolesKey,
		"arguments_json":   action.ArgumentsJSON,
		"confirmed":        action.Confirmed,
	}
	_, err := g.DB().Model("pending_actions").Ctx(ctx).Data(data).Insert()
	return err
}

func (s *sqliteActionStore) Load(ctx context.Context, token string) (pendingAction, error) {
	var row sqliteActionRow
	err := g.DB().Model("pending_actions").Ctx(ctx).
		Fields("token", "tool_name", "operation_id", "arguments_hash", "risk", "requires_confirm", "expires_at", "user_id", "tenant_id", "roles_key", "arguments_json", "confirmed").
		Where("token", token).
		Scan(&row)
	if errors.Is(err, sql.ErrNoRows) || row.Token == "" {
		return pendingAction{}, sql.ErrNoRows
	}
	if err != nil {
		return pendingAction{}, err
	}
	return pendingAction{
		PreparedAction: PreparedAction{
			Token:           row.Token,
			ToolName:        row.ToolName,
			OperationID:     row.OperationID,
			ArgumentsHash:   row.ArgumentsHash,
			Risk:            row.Risk,
			RequiresConfirm: row.RequiresConfirm,
			ExpiresAt:       row.ExpiresAt,
		},
		UserID:        row.UserID,
		TenantID:      row.TenantID,
		RolesKey:      row.RolesKey,
		ArgumentsJSON: row.ArgumentsJSON,
		Confirmed:     row.Confirmed,
	}, nil
}

func (s *sqliteActionStore) Confirm(ctx context.Context, token string) error {
	result, err := g.DB().Model("pending_actions").Ctx(ctx).
		Where("token", token).
		Data(g.Map{"confirmed": true}).
		Update()
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *sqliteActionStore) ConsumeConfirmed(ctx context.Context, token string, now time.Time) error {
	result, err := g.DB().Model("pending_actions").Ctx(ctx).
		Where("token", token).
		Where("confirmed", true).
		Where("expires_at >= ?", now).
		Delete()
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *sqliteActionStore) Delete(ctx context.Context, token string) error {
	_, err := g.DB().Model("pending_actions").Ctx(ctx).Where("token", token).Delete()
	return err
}

func (s *sqliteActionStore) Prune(ctx context.Context, now time.Time) error {
	_, err := g.DB().Model("pending_actions").Ctx(ctx).Where("expires_at < ?", now).Delete()
	return err
}

func (s *sqliteActionStore) SaveOperation(ctx context.Context, operation OperationRecord) error {
	_, err := g.DB().Model("tool_operations").Ctx(ctx).Data(g.Map{
		"operation_id":    operation.OperationID,
		"conversation_id": operation.ConversationID,
		"user_id":         operation.UserID,
		"tenant_id":       operation.TenantID,
		"tool_name":       operation.ToolName,
		"status":          operation.Status,
		"result":          operation.ResultSummary,
		"evidence_id":     operation.EvidenceID,
		"created_at":      operation.UpdatedAt,
		"updated_at":      operation.UpdatedAt,
	}).Insert()
	return err
}

func (s *sqliteActionStore) UpdateOperation(ctx context.Context, operation OperationRecord) error {
	_, err := g.DB().Model("tool_operations").Ctx(ctx).
		Where("operation_id", operation.OperationID).
		Data(g.Map{
			"status":      operation.Status,
			"result":      operation.ResultSummary,
			"evidence_id": operation.EvidenceID,
			"updated_at":  operation.UpdatedAt,
		}).Update()
	return err
}

func (s *sqliteActionStore) LoadOperation(ctx context.Context, operationID string) (OperationRecord, error) {
	var row sqliteOperationRow
	err := g.DB().Model("tool_operations").Ctx(ctx).
		Fields("operation_id", "conversation_id", "user_id", "tenant_id", "tool_name", "status", "result", "evidence_id", "updated_at").
		Where("operation_id", operationID).Scan(&row)
	if errors.Is(err, sql.ErrNoRows) || row.OperationID == "" {
		return OperationRecord{}, sql.ErrNoRows
	}
	if err != nil {
		return OperationRecord{}, err
	}
	return OperationRecord{
		OperationID:    row.OperationID,
		ConversationID: row.ConversationID,
		UserID:         row.UserID,
		TenantID:       row.TenantID,
		ToolName:       row.ToolName,
		Status:         OperationStatus(row.Status),
		ResultSummary:  row.Result,
		EvidenceID:     row.EvidenceID,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}
