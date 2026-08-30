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

func ListProfileSlots(ctx context.Context, principal MemoryPrincipal, limit int) ([]ProfileSlot, error) {
	if err := principal.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var profiles []ProfileSlot
	err := g.DB().Model("profile_slots").Ctx(ctx).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		WhereIn("status", []string{profileStatusActive, profileStatusShadow}).
		OrderAsc("slot_key").OrderDesc("revision").
		Limit(limit).
		Scan(&profiles)
	return profiles, err
}

func ListProfileCandidates(ctx context.Context, principal MemoryPrincipal, limit int) ([]ProfileCandidate, error) {
	if err := principal.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var candidates []ProfileCandidate
	err := g.DB().Model("profile_candidates").Ctx(ctx).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		Where("status", "pending").
		WhereGT("expires_at", time.Now().UTC()).
		OrderDesc("updated_at").Limit(limit).
		Scan(&candidates)
	return candidates, err
}

func ConfirmProfileCandidate(ctx context.Context, principal MemoryPrincipal, candidateID int64, expectedRevision int) (ProfileSlot, error) {
	if err := principal.Validate(); err != nil {
		return ProfileSlot{}, err
	}
	if candidateID <= 0 || expectedRevision <= 0 {
		return ProfileSlot{}, fmt.Errorf("candidate id and revision are required")
	}
	var saved ProfileSlot
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var candidate ProfileCandidate
		err := tx.Model("profile_candidates").Ctx(ctx).
			Where("id", candidateID).
			Where("tenant_id", principal.TenantID).
			Where("user_id", principal.UserID).
			Where("status", "pending").
			Scan(&candidate)
		if errors.Is(err, sql.ErrNoRows) || candidate.ID == 0 {
			return sql.ErrNoRows
		}
		if err != nil {
			return err
		}
		if candidate.Revision != expectedRevision {
			return fmt.Errorf("profile candidate revision conflict")
		}
		now := time.Now().UTC()
		if _, err = tx.Model("profile_slots").Ctx(ctx).
			Where("tenant_id", principal.TenantID).
			Where("user_id", principal.UserID).
			Where("slot_key", candidate.SlotKey).
			Where("status", profileStatusActive).
			Data(g.Map{"status": profileStatusRevoked, "valid_until": now, "updated_at": now}).Update(); err != nil {
			return err
		}
		result, err := tx.Model("profile_slots").Ctx(ctx).Data(g.Map{
			"tenant_id": candidate.TenantID, "user_id": candidate.UserID,
			"slot_key": candidate.SlotKey, "value_type": candidate.ValueType,
			"value_json": candidate.ValueJSON, "value_text": candidate.ValueText,
			"revision": candidate.Revision, "status": profileStatusActive,
			"confirmation": "confirmed_by_user", "confidence": candidate.Confidence,
			"source_conversation_id": candidate.SourceConversationID,
			"source_turn_ids":        candidate.SourceTurnIDs, "source_excerpt": candidate.SourceExcerpt,
			"source_hash": candidate.SourceHash, "valid_from": now,
			"created_at": now, "updated_at": now,
		}).Insert()
		if err != nil {
			return err
		}
		profileID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.Model("profile_candidates").Ctx(ctx).Where("id", candidate.ID).
			Where("status", "pending").Data(g.Map{"status": "confirmed", "updated_at": now}).Update(); err != nil {
			return err
		}
		return tx.Model("profile_slots").Ctx(ctx).Where("id", profileID).Scan(&saved)
	})
	if err == nil {
		recordMemoryMutation(ctx, "confirm_profile")
	}
	return saved, err
}

func RejectProfileCandidate(ctx context.Context, principal MemoryPrincipal, candidateID int64, expectedRevision int) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	result, err := g.DB().Model("profile_candidates").Ctx(ctx).
		Where("id", candidateID).
		Where("tenant_id", principal.TenantID).
		Where("user_id", principal.UserID).
		Where("revision", expectedRevision).
		Where("status", "pending").
		Data(g.Map{"status": "rejected", "source_excerpt": "", "updated_at": time.Now().UTC()}).
		Update()
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("profile candidate revision conflict")
	}
	recordMemoryMutation(ctx, "reject_profile")
	return nil
}

func DeleteProfileSlot(ctx context.Context, principal MemoryPrincipal, profileID int64, expectedRevision int) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var profile ProfileSlot
		err := tx.Model("profile_slots").Ctx(ctx).
			Where("id", profileID).Where("tenant_id", principal.TenantID).Where("user_id", principal.UserID).
			Scan(&profile)
		if errors.Is(err, sql.ErrNoRows) || profile.ID == 0 {
			return sql.ErrNoRows
		}
		if err != nil {
			return err
		}
		if profile.Revision != expectedRevision {
			return fmt.Errorf("profile revision conflict")
		}
		if _, err = tx.Model("profile_slots").Ctx(ctx).Where("id", profile.ID).Data(g.Map{
			"status": profileStatusRevoked, "valid_until": now,
			"value_json": "null", "value_text": "", "source_excerpt": "", "updated_at": now,
		}).Update(); err != nil {
			return err
		}
		return addForgetTombstone(ctx, tx, principal, "profile_slot", profile.SlotKey, now)
	})
	if err == nil {
		recordMemoryMutation(ctx, "delete_profile")
	}
	return err
}

func ListMemoryEvents(ctx context.Context, principal MemoryPrincipal, limit int) ([]MemoryEvent, error) {
	if err := principal.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var events []MemoryEvent
	err := g.DB().Model("memory_events").Ctx(ctx).
		Where("tenant_id", principal.TenantID).Where("user_id", principal.UserID).
		WhereIn("status", []string{profileStatusActive, profileStatusShadow}).
		OrderDesc("recorded_at").Limit(limit).Scan(&events)
	return events, err
}

func DeleteMemoryEvent(ctx context.Context, principal MemoryPrincipal, eventID string) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return fmt.Errorf("event id is required")
	}
	now := time.Now().UTC()
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, err := tx.Model("memory_events").Ctx(ctx).
			Where("id", eventID).Where("tenant_id", principal.TenantID).Where("user_id", principal.UserID).
			Data(g.Map{
				"status": "deleted", "summary": "", "tags_json": "[]", "entities_json": "[]",
				"vector": "", "vector_status": "deleted", "evidence_ids": "[]", "updated_at": now,
			}).Update()
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return sql.ErrNoRows
		}
		return addForgetTombstone(ctx, tx, principal, "event", eventID, now)
	})
	if err == nil {
		recordMemoryMutation(ctx, "delete_event")
	}
	return err
}

func addForgetTombstone(ctx context.Context, tx gdb.TX, principal MemoryPrincipal, scopeKind string, scopeValue string, now time.Time) error {
	var row struct {
		Generation int `json:"generation"`
	}
	if err := tx.Model("memory_forget_tombstones").Ctx(ctx).Fields("generation").
		Where("tenant_id", principal.TenantID).Where("user_id", principal.UserID).
		OrderDesc("generation").Limit(1).Scan(&row); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	generation := row.Generation + 1
	if _, err := tx.Model("memory_forget_tombstones").Ctx(ctx).Data(g.Map{
		"tenant_id": principal.TenantID, "user_id": principal.UserID,
		"scope_kind": scopeKind, "scope_value": scopeValue,
		"generation": generation, "created_at": now,
	}).Insert(); err != nil {
		return err
	}
	_, err := tx.Model("memory_buffers").Ctx(ctx).
		Where("tenant_id", principal.TenantID).Where("user_id", principal.UserID).
		Data(g.Map{
			"generation": generation, "pending_turn_ids": "[]", "pending_tokens": 0,
			"claimed_turn_ids": "[]", "claimed_tokens": 0, "explicit_flush": false,
			"status": memoryBufferProcessed, "lease_until": nil, "updated_at": now,
		}).Update()
	return err
}
