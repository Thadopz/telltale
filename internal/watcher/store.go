package watcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

type Store interface {
	LoadState(context.Context, string) (DetectorState, error)
	SaveState(context.Context, DetectorState) error
	SaveIncident(context.Context, Incident) error
}

type MySQLStore struct{}

func NewMySQLStore() *MySQLStore {
	return &MySQLStore{}
}

func (s *MySQLStore) LoadState(ctx context.Context, stateKey string) (DetectorState, error) {
	var state DetectorState
	err := g.DB().Model("detector_states").
		Ctx(ctx).
		Fields(
			"state_key",
			"rule_id",
			"state",
			"consecutive_failures",
			"consecutive_successes",
			"last_value",
			"last_evidence_id",
			"last_error",
			"updated_at",
		).
		Where("state_key", stateKey).
		Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return DetectorState{StateKey: stateKey, State: StateNormal}, nil
	}
	if err != nil {
		return DetectorState{}, fmt.Errorf("load detector state %s: %w", stateKey, err)
	}
	if state.State == "" {
		state.StateKey = stateKey
		state.State = StateNormal
	}
	return state, nil
}

func (s *MySQLStore) SaveState(ctx context.Context, state DetectorState) error {
	data := g.Map{
		"state_key":             state.StateKey,
		"rule_id":               state.RuleID,
		"state":                 state.State,
		"consecutive_failures":  state.ConsecutiveFailures,
		"consecutive_successes": state.ConsecutiveSuccesses,
		"last_value":            state.LastValue,
		"last_evidence_id":      nullableString(state.LastEvidenceID),
		"last_error":            nullableString(state.LastError),
		"updated_at":            state.UpdatedAt,
	}
	if _, err := g.DB().Model("detector_states").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return fmt.Errorf("insert detector state %s: %w", state.StateKey, err)
	}
	_, err := g.DB().Model("detector_states").
		Ctx(ctx).
		Where("state_key", state.StateKey).
		Data(data).
		Update()
	if err != nil {
		return fmt.Errorf("update detector state %s: %w", state.StateKey, err)
	}
	return nil
}

func (s *MySQLStore) SaveIncident(ctx context.Context, incident Incident) error {
	data := g.Map{
		"id":            incident.ID,
		"fingerprint":   incident.Fingerprint,
		"rule_id":       incident.RuleID,
		"domain":        incident.Domain,
		"status":        incident.Status,
		"severity":      incident.Severity,
		"first_seen_at": incident.FirstSeenAt,
		"last_seen_at":  incident.LastSeenAt,
		"resolved_at":   incident.ResolvedAt,
		"latest_value":  incident.LatestValue,
		"evidence_id":   nullableString(incident.EvidenceID),
		"evidence_json": nullableString(incident.EvidenceJSON),
	}
	if _, err := g.DB().Model("incidents").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return fmt.Errorf("insert incident %s: %w", incident.ID, err)
	}

	update := g.Map{
		"status":        incident.Status,
		"severity":      incident.Severity,
		"last_seen_at":  incident.LastSeenAt,
		"resolved_at":   incident.ResolvedAt,
		"latest_value":  incident.LatestValue,
		"evidence_id":   nullableString(incident.EvidenceID),
		"evidence_json": nullableString(incident.EvidenceJSON),
	}
	_, err := g.DB().Model("incidents").
		Ctx(ctx).
		Where("fingerprint", incident.Fingerprint).
		Data(update).
		Update()
	if err != nil {
		return fmt.Errorf("update incident %s: %w", incident.ID, err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func resolvedTime(transition string, now time.Time) *time.Time {
	if transition != TransitionResolved {
		return nil
	}
	resolvedAt := now
	return &resolvedAt
}
