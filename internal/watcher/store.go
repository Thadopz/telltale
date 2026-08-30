package watcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

type Store interface {
	LoadState(context.Context, string) (DetectorState, error)
	SaveState(context.Context, DetectorState) error
	CommitEvaluation(context.Context, DetectorState, *Incident, *DiagnosisJob) error
}

type watcherDB interface {
	Model(tableNameOrStruct ...interface{}) *gdb.Model
}

type SQLiteStore struct{}

func NewSQLiteStore() *SQLiteStore {
	return &SQLiteStore{}
}

func (s *SQLiteStore) LoadState(ctx context.Context, stateKey string) (DetectorState, error) {
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

func (s *SQLiteStore) SaveState(ctx context.Context, state DetectorState) error {
	return saveStateWithDB(ctx, g.DB(), state)
}

// CommitEvaluation keeps the detector state, incident transition, and optional
// diagnosis job in one transaction. A failed commit is retried by the next
// watcher cycle without leaving a half-created diagnosis.
func (s *SQLiteStore) CommitEvaluation(ctx context.Context, state DetectorState, incident *Incident, job *DiagnosisJob) error {
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := saveStateWithDB(ctx, tx, state); err != nil {
			return err
		}
		if incident != nil {
			if err := saveIncidentWithDB(ctx, tx, *incident); err != nil {
				return err
			}
		}
		if job != nil {
			if err := saveDiagnosisJobWithDB(ctx, tx, *job); err != nil {
				return err
			}
		}
		return nil
	})
}

func saveStateWithDB(ctx context.Context, db watcherDB, state DetectorState) error {
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
	if _, err := db.Model("detector_states").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return fmt.Errorf("insert detector state %s: %w", state.StateKey, err)
	}
	if _, err := db.Model("detector_states").Ctx(ctx).Where("state_key", state.StateKey).Data(data).Update(); err != nil {
		return fmt.Errorf("update detector state %s: %w", state.StateKey, err)
	}
	return nil
}

func saveIncidentWithDB(ctx context.Context, db watcherDB, incident Incident) error {
	insert := g.Map{
		"id":            incident.ID,
		"fingerprint":   incident.Fingerprint,
		"rule_id":       incident.RuleID,
		"domain":        incident.Domain,
		"service":       incident.Service,
		"environment":   incident.Environment,
		"status":        incident.Status,
		"severity":      incident.Severity,
		"first_seen_at": incident.FirstSeenAt,
		"last_seen_at":  incident.LastSeenAt,
		"resolved_at":   incident.ResolvedAt,
		"latest_value":  incident.LatestValue,
		"evidence_id":   nullableString(incident.EvidenceID),
		"evidence_json": nullableString(incident.EvidenceJSON),
	}
	if _, err := db.Model("incidents").Ctx(ctx).Data(insert).InsertIgnore(); err != nil {
		return fmt.Errorf("insert incident %s: %w", incident.ID, err)
	}
	update := g.Map{
		"rule_id":       incident.RuleID,
		"domain":        incident.Domain,
		"service":       incident.Service,
		"environment":   incident.Environment,
		"status":        incident.Status,
		"severity":      incident.Severity,
		"last_seen_at":  incident.LastSeenAt,
		"resolved_at":   incident.ResolvedAt,
		"latest_value":  incident.LatestValue,
		"evidence_id":   nullableString(incident.EvidenceID),
		"evidence_json": nullableString(incident.EvidenceJSON),
		"updated_at":    time.Now().UTC(),
	}
	if _, err := db.Model("incidents").Ctx(ctx).Where("fingerprint", incident.Fingerprint).Data(update).Update(); err != nil {
		return fmt.Errorf("update incident %s: %w", incident.ID, err)
	}
	return nil
}

func saveDiagnosisJobWithDB(ctx context.Context, db watcherDB, job DiagnosisJob) error {
	requestJSON, err := json.Marshal(job.Request)
	if err != nil {
		return fmt.Errorf("marshal diagnosis job %s request: %w", job.ID, err)
	}
	data := g.Map{
		"id":                job.ID,
		"incident_id":       job.IncidentID,
		"episode_opened_at": job.EpisodeOpenedAt,
		"request_json":      string(requestJSON),
		"status":            DiagnosisJobPending,
		"attempt_count":     0,
		"next_attempt_at":   job.NextAttemptAt,
		"conversation_id":   job.ConversationID,
		"updated_at":        time.Now().UTC(),
	}
	if _, err := db.Model("incident_diagnosis_jobs").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return fmt.Errorf("insert incident diagnosis job %s: %w", job.ID, err)
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
