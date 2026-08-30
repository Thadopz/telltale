package watcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	maxDiagnosisResultBytes = 256 * 1024
	maxDiagnosisAnswerBytes = 64 * 1024
	maxDiagnosisErrorRunes  = 2048
)

type diagnosisJobRow struct {
	ID              string     `json:"id"`
	IncidentID      string     `json:"incident_id"`
	EpisodeOpenedAt time.Time  `json:"episode_opened_at"`
	RequestJSON     string     `json:"request_json"`
	Status          string     `json:"status"`
	AttemptCount    int        `json:"attempt_count"`
	NextAttemptAt   time.Time  `json:"next_attempt_at"`
	LeaseUntil      *time.Time `json:"lease_until"`
	ConversationID  string     `json:"conversation_id"`
	RunID           string     `json:"run_id"`
	ResultJSON      string     `json:"result_json"`
	LastError       string     `json:"last_error"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (s *SQLiteStore) ClaimDiagnosisJob(ctx context.Context, now time.Time, leaseDuration time.Duration, maxAttempts int) (*DiagnosisJob, error) {
	var claimed *DiagnosisJob
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Model("incident_diagnosis_jobs").
			Ctx(ctx).
			Where("status", DiagnosisJobRunning).
			WhereLTE("lease_until", now).
			WhereGTE("attempt_count", maxAttempts).
			Data(g.Map{
				"status":       DiagnosisJobFailed,
				"lease_until":  nil,
				"last_error":   "diagnosis lease expired after maximum attempts",
				"completed_at": now,
				"updated_at":   now,
			}).
			Update(); err != nil {
			return fmt.Errorf("finalize expired diagnosis jobs: %w", err)
		}

		var row diagnosisJobRow
		readyCondition := "((status = ? AND next_attempt_at <= ? AND attempt_count < ?) OR (status = ? AND lease_until <= ? AND attempt_count < ?))"
		err := tx.Model("incident_diagnosis_jobs").
			Ctx(ctx).
			Where(readyCondition, DiagnosisJobPending, now, maxAttempts, DiagnosisJobRunning, now, maxAttempts).
			OrderAsc("next_attempt_at").
			OrderAsc("created_at").
			Limit(1).
			Scan(&row)
		if errors.Is(err, sql.ErrNoRows) || row.ID == "" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find ready diagnosis job: %w", err)
		}

		leaseUntil := now.Add(leaseDuration)
		startedAt := row.StartedAt
		if startedAt == nil {
			startedAt = &now
		}
		result, err := tx.Model("incident_diagnosis_jobs").
			Ctx(ctx).
			Where("id", row.ID).
			Where(readyCondition, DiagnosisJobPending, now, maxAttempts, DiagnosisJobRunning, now, maxAttempts).
			Data(g.Map{
				"status":        DiagnosisJobRunning,
				"attempt_count": row.AttemptCount + 1,
				"lease_until":   leaseUntil,
				"started_at":    startedAt,
				"updated_at":    now,
			}).
			Update()
		if err != nil {
			return fmt.Errorf("claim diagnosis job %s: %w", row.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read diagnosis job claim result: %w", err)
		}
		if affected != 1 {
			return nil
		}

		row.Status = DiagnosisJobRunning
		row.AttemptCount++
		row.LeaseUntil = &leaseUntil
		row.StartedAt = startedAt
		job, err := diagnosisJobFromRow(row)
		if err != nil {
			return err
		}
		status, err := incidentStatusWithDB(ctx, tx, row.IncidentID)
		if err != nil {
			return err
		}
		job.Request.CurrentStatus = status
		claimed = &job
		return nil
	})
	return claimed, err
}

func (s *SQLiteStore) CompleteDiagnosisJob(ctx context.Context, job DiagnosisJob, result diagnosis.DiagnoseResult, now time.Time) error {
	resultJSON, err := marshalBoundedDiagnosisResult(result)
	if err != nil {
		return fmt.Errorf("marshal diagnosis result for job %s: %w", job.ID, err)
	}
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		update, err := tx.Model("incident_diagnosis_jobs").
			Ctx(ctx).
			Where("id", job.ID).
			Where("status", DiagnosisJobRunning).
			Data(g.Map{
				"status":       DiagnosisJobSucceeded,
				"lease_until":  nil,
				"run_id":       nullableString(result.RunID),
				"result_json":  resultJSON,
				"last_error":   nil,
				"completed_at": now,
				"updated_at":   now,
			}).
			Update()
		if err != nil {
			return fmt.Errorf("complete diagnosis job %s: %w", job.ID, err)
		}
		affected, err := update.RowsAffected()
		if err != nil || affected != 1 {
			return fmt.Errorf("diagnosis job %s is no longer running", job.ID)
		}

		var newest struct {
			EpisodeOpenedAt time.Time `json:"episode_opened_at"`
		}
		if err := tx.Model("incident_diagnosis_jobs").
			Ctx(ctx).
			Fields("episode_opened_at").
			Where("incident_id", job.IncidentID).
			Where("status", DiagnosisJobSucceeded).
			OrderDesc("episode_opened_at").
			Limit(1).
			Scan(&newest); err != nil {
			return fmt.Errorf("find newest diagnosis for incident %s: %w", job.IncidentID, err)
		}
		if !newest.EpisodeOpenedAt.Equal(job.EpisodeOpenedAt) {
			return nil
		}
		if _, err := tx.Model("incidents").Ctx(ctx).Where("id", job.IncidentID).Data(g.Map{
			"diagnosis":  resultJSON,
			"updated_at": now,
		}).Update(); err != nil {
			return fmt.Errorf("update incident %s diagnosis: %w", job.IncidentID, err)
		}
		return nil
	})
}

func (s *SQLiteStore) FailDiagnosisJob(ctx context.Context, job DiagnosisJob, runErr error, now time.Time, retryDelays []time.Duration) (string, *time.Time, error) {
	errorText := boundedError(runErr)
	status := DiagnosisJobFailed
	var nextAttemptAt *time.Time
	if job.AttemptCount <= len(retryDelays) {
		next := now.Add(retryDelays[job.AttemptCount-1])
		nextAttemptAt = &next
		status = DiagnosisJobPending
	}
	data := g.Map{
		"status":      status,
		"lease_until": nil,
		"last_error":  errorText,
		"updated_at":  now,
	}
	if nextAttemptAt != nil {
		data["next_attempt_at"] = *nextAttemptAt
		data["completed_at"] = nil
	} else {
		data["completed_at"] = now
	}
	result, err := g.DB().Model("incident_diagnosis_jobs").
		Ctx(ctx).
		Where("id", job.ID).
		Where("status", DiagnosisJobRunning).
		Data(data).
		Update()
	if err != nil {
		return "", nil, fmt.Errorf("record diagnosis job %s failure: %w", job.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return "", nil, fmt.Errorf("diagnosis job %s is no longer running", job.ID)
	}
	return status, nextAttemptAt, nil
}

func (s *SQLiteStore) RequeueCanceledDiagnosisJob(ctx context.Context, job DiagnosisJob, now time.Time) error {
	attemptCount := job.AttemptCount - 1
	if attemptCount < 0 {
		attemptCount = 0
	}
	result, err := g.DB().Model("incident_diagnosis_jobs").
		Ctx(ctx).
		Where("id", job.ID).
		Where("status", DiagnosisJobRunning).
		Data(g.Map{
			"status":          DiagnosisJobPending,
			"attempt_count":   attemptCount,
			"next_attempt_at": now,
			"lease_until":     nil,
			"updated_at":      now,
		}).
		Update()
	if err != nil {
		return fmt.Errorf("requeue canceled diagnosis job %s: %w", job.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("diagnosis job %s is no longer running", job.ID)
	}
	return nil
}

func (s *SQLiteStore) ListIncidents(ctx context.Context, query IncidentQuery) ([]Incident, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	model := g.DB().Model("incidents").Ctx(ctx)
	if query.Status != "" {
		model = model.Where("status", query.Status)
	}
	if query.Service != "" {
		model = model.Where("service", query.Service)
	}
	var incidents []Incident
	if err := model.OrderDesc("last_seen_at").Limit(limit).Scan(&incidents); err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return incidents, nil
}

func (s *SQLiteStore) GetIncident(ctx context.Context, incidentID string) (IncidentDetail, error) {
	var incident Incident
	if err := g.DB().Model("incidents").Ctx(ctx).Where("id", incidentID).Scan(&incident); err != nil {
		return IncidentDetail{}, fmt.Errorf("get incident %s: %w", incidentID, err)
	}
	if incident.ID == "" {
		return IncidentDetail{}, sql.ErrNoRows
	}
	var rows []diagnosisJobRow
	if err := g.DB().Model("incident_diagnosis_jobs").
		Ctx(ctx).
		Where("incident_id", incidentID).
		OrderDesc("episode_opened_at").
		Limit(20).
		Scan(&rows); err != nil {
		return IncidentDetail{}, fmt.Errorf("list diagnosis jobs for incident %s: %w", incidentID, err)
	}
	jobs := make([]DiagnosisJob, 0, len(rows))
	for _, row := range rows {
		job, err := diagnosisJobFromRow(row)
		if err != nil {
			return IncidentDetail{}, err
		}
		jobs = append(jobs, job)
	}
	return IncidentDetail{Incident: incident, Jobs: jobs}, nil
}

func incidentStatusWithDB(ctx context.Context, db watcherDB, incidentID string) (string, error) {
	var row struct {
		Status string `json:"status"`
	}
	if err := db.Model("incidents").Ctx(ctx).Fields("status").Where("id", incidentID).Scan(&row); err != nil {
		return "", fmt.Errorf("get incident %s status: %w", incidentID, err)
	}
	if row.Status == "" {
		return "", fmt.Errorf("incident %s not found", incidentID)
	}
	return row.Status, nil
}

func diagnosisJobFromRow(row diagnosisJobRow) (DiagnosisJob, error) {
	var request diagnosis.IncidentDiagnoseRequest
	if err := json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
		return DiagnosisJob{}, fmt.Errorf("parse diagnosis job %s request: %w", row.ID, err)
	}
	return DiagnosisJob{
		ID:              row.ID,
		IncidentID:      row.IncidentID,
		EpisodeOpenedAt: row.EpisodeOpenedAt,
		Request:         request,
		Status:          row.Status,
		AttemptCount:    row.AttemptCount,
		NextAttemptAt:   row.NextAttemptAt,
		LeaseUntil:      row.LeaseUntil,
		ConversationID:  row.ConversationID,
		RunID:           row.RunID,
		ResultJSON:      row.ResultJSON,
		LastError:       row.LastError,
		StartedAt:       row.StartedAt,
		CompletedAt:     row.CompletedAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}

func marshalBoundedDiagnosisResult(result diagnosis.DiagnoseResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	if len(encoded) <= maxDiagnosisResultBytes {
		return string(encoded), nil
	}
	summary := diagnosis.DiagnoseResult{
		Answer:         truncateBytes(result.Answer, maxDiagnosisAnswerBytes),
		RunID:          result.RunID,
		ConversationID: result.ConversationID,
		EvidenceIDs:    limitStrings(result.EvidenceIDs, 64),
		FailedQueryIDs: limitStrings(result.FailedQueryIDs, 64),
		TraceSummary: diagnosis.TraceSummary{
			StepCount:       result.TraceSummary.StepCount,
			EvidenceCount:   result.TraceSummary.EvidenceCount,
			FailedToolCount: result.TraceSummary.FailedToolCount,
		},
	}
	encoded, err = json.Marshal(summary)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxDiagnosisResultBytes {
		return "", fmt.Errorf("bounded diagnosis result exceeds %d bytes", maxDiagnosisResultBytes)
	}
	return string(encoded), nil
}

func boundedError(err error) string {
	if err == nil {
		return "diagnosis failed"
	}
	text := strings.TrimSpace(err.Error())
	if utf8.RuneCountInString(text) <= maxDiagnosisErrorRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxDiagnosisErrorRunes])
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	truncated := value[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append([]string(nil), values[:limit]...)
}
