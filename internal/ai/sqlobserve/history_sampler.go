package sqlobserve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"

	"github.com/Thadopz/telltale/internal/observability"
)

const (
	defaultHistorySampleInterval = time.Minute
	defaultHistoryRetention      = 48 * time.Hour
	defaultHistoryMaxStatements  = 50
	historyQueryTimeout          = 5 * time.Second
	historyPreviewCharacters     = 1000
)

type ObservationHistoryConfig struct {
	Enabled                bool
	SampleInterval         time.Duration
	Retention              time.Duration
	MaxStatementsPerSample int
	Targets                []HistoryTarget
}

type HistoryTarget struct {
	Name   string
	Engine DatabaseEngine
}

type HistorySampler struct {
	config      ObservationHistoryConfig
	owner       string
	lastCleanup time.Time
}

type pgStatementSnapshot struct {
	Fingerprint string
	Database    string
	Preview     string
	Calls       int64
	TotalExecMS float64
	Rows        int64
	ObservedAt  time.Time
	StatsReset  time.Time
}

type statementCheckpoint struct {
	Fingerprint string    `json:"statement_fingerprint"`
	Calls       int64     `json:"calls_total"`
	TotalExecMS float64   `json:"total_exec_ms_total"`
	Rows        int64     `json:"rows_total"`
	ObservedAt  time.Time `json:"observed_at"`
	ResetAt     time.Time `json:"reset_at"`
}

func NewConfiguredHistorySampler(ctx context.Context) (*HistorySampler, error) {
	config, err := LoadObservationHistoryConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &HistorySampler{config: config, owner: uuid.NewString()}, nil
}

func LoadObservationHistoryConfig(ctx context.Context) (ObservationHistoryConfig, error) {
	config := ObservationHistoryConfig{
		SampleInterval:         defaultHistorySampleInterval,
		Retention:              defaultHistoryRetention,
		MaxStatementsPerSample: defaultHistoryMaxStatements,
	}
	config.Enabled = optionalConfigBool(ctx, "database.observation_history.enabled")
	if seconds := optionalConfigInt(ctx, "database.observation_history.sample_interval_seconds"); seconds > 0 {
		config.SampleInterval = time.Duration(seconds) * time.Second
	}
	if hours := optionalConfigInt(ctx, "database.observation_history.retention_hours"); hours > 0 {
		config.Retention = time.Duration(hours) * time.Hour
	}
	if maximum := optionalConfigInt(ctx, "database.observation_history.max_statements_per_sample"); maximum > 0 {
		if maximum > defaultHistoryMaxStatements {
			maximum = defaultHistoryMaxStatements
		}
		config.MaxStatementsPerSample = maximum
	}
	if !config.Enabled {
		return config, nil
	}

	value, err := g.Cfg().Get(ctx, "database.targets")
	if err != nil || value == nil || value.IsEmpty() {
		return config, fmt.Errorf("database observation history is enabled but no targets are configured")
	}
	for name, raw := range value.Map() {
		targetConfig, ok := raw.(map[string]any)
		if !ok {
			if converted, convertOK := raw.(g.Map); convertOK {
				targetConfig = converted
			} else {
				continue
			}
		}
		if !valueAsBool(targetConfig["history_enabled"]) {
			continue
		}
		engine, engineErr := configuredDatabaseEngine(ctx, name)
		if engineErr != nil {
			return config, engineErr
		}
		if engine != EnginePostgreSQL {
			return config, fmt.Errorf("database target %q enables history but engine %q is not supported by the sampler", name, engine)
		}
		config.Targets = append(config.Targets, HistoryTarget{Name: name, Engine: engine})
	}
	if len(config.Targets) == 0 {
		return config, fmt.Errorf("database observation history is enabled but no PostgreSQL target has history_enabled=true")
	}
	return config, nil
}

func (sampler *HistorySampler) Enabled() bool {
	return sampler != nil && sampler.config.Enabled && len(sampler.config.Targets) > 0
}

func (sampler *HistorySampler) Run(ctx context.Context) error {
	if !sampler.Enabled() {
		return nil
	}
	if _, err := sampler.RunOnce(ctx); err != nil {
		g.Log().Warningf(ctx, "database history sample failed: %v", err)
	}
	ticker := time.NewTicker(sampler.config.SampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := sampler.RunOnce(ctx); err != nil {
				g.Log().Warningf(ctx, "database history sample failed: %v", err)
			}
		}
	}
}

func (sampler *HistorySampler) RunOnce(ctx context.Context) (int, error) {
	if !sampler.Enabled() {
		return 0, nil
	}
	completed := 0
	var failures []error
	for _, target := range sampler.config.Targets {
		ok, err := sampler.acquireLease(ctx, target.Name)
		if err != nil {
			failures = append(failures, safeHistoryError("acquire history lease target="+target.Name, err))
			continue
		}
		if !ok {
			continue
		}
		startedAt := time.Now()
		statementCount, sampleErr := sampler.sampleTarget(ctx, target)
		observability.RecordDatabaseHistorySample(ctx, target.Name, statementCount, time.Since(startedAt), sampleErr)
		if sampleErr != nil {
			_ = recordHistoryTargetStatus(ctx, target, sampleErr)
			failures = append(failures, fmt.Errorf("sample database history target=%s: %w", target.Name, sampleErr))
			continue
		}
		_ = recordHistoryTargetStatus(ctx, target, nil)
		completed++
	}
	if sampler.lastCleanup.IsZero() || time.Since(sampler.lastCleanup) >= time.Hour {
		if err := sampler.cleanup(ctx); err != nil {
			failures = append(failures, safeHistoryError("clean database history", err))
		} else {
			sampler.lastCleanup = time.Now().UTC()
		}
	}
	return completed, errors.Join(failures...)
}

func recordHistoryTargetStatus(ctx context.Context, target HistoryTarget, sampleErr error) error {
	now := time.Now().UTC()
	status := "available"
	errorCode := ""
	lastError := ""
	lastSuccess := any(now)
	if sampleErr != nil {
		status = "unavailable"
		errorCode = historyErrorCode(sampleErr)
		lastError = boundedText(sampleErr.Error(), 512)
		lastSuccess = nil
	}
	_, err := g.DB().Exec(ctx, `INSERT INTO database_observation_status(
target, engine, status, error_code, last_error, last_success_at, last_attempt_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(target) DO UPDATE SET engine=excluded.engine, status=excluded.status, error_code=excluded.error_code,
last_error=excluded.last_error, last_success_at=COALESCE(excluded.last_success_at, database_observation_status.last_success_at),
last_attempt_at=excluded.last_attempt_at, updated_at=excluded.updated_at`,
		target.Name, string(target.Engine), status, errorCode, lastError, lastSuccess, now, now)
	return err
}

func historyErrorCode(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "permission") || strings.Contains(message, "denied") {
		return "permission_denied"
	}
	if strings.Contains(message, "pg_stat_statements") || strings.Contains(message, "does not exist") {
		return "pg_stat_statements_unavailable"
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") {
		return "sample_timeout"
	}
	return "sample_failed"
}

func safeHistoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", operation, historyErrorCode(err))
}

func (sampler *HistorySampler) acquireLease(ctx context.Context, target string) (bool, error) {
	now := time.Now().UTC()
	leaseUntil := now.Add(sampler.config.SampleInterval + 30*time.Second)
	result, err := g.DB().Exec(ctx, `INSERT INTO database_observation_leases(target, owner, lease_until, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(target) DO UPDATE SET owner=excluded.owner, lease_until=excluded.lease_until, updated_at=excluded.updated_at
WHERE database_observation_leases.lease_until <= ? OR database_observation_leases.owner = ?`,
		target, sampler.owner, leaseUntil, now, now, sampler.owner)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (sampler *HistorySampler) sampleTarget(ctx context.Context, target HistoryTarget) (int, error) {
	queryContext, cancel := context.WithTimeout(ctx, historyQueryTimeout)
	defer cancel()
	executor, err := openConfiguredPostgreSQLExecutor(queryContext, target.Name)
	if err != nil {
		return 0, safeHistoryError("open PostgreSQL history source", err)
	}
	defer executor.Close()
	rows, err := executor.Query(queryContext, `SELECT
  CURRENT_TIMESTAMP AS observed_at,
  i.stats_reset AS stats_reset,
  d.datname AS database_name,
  s.queryid::text AS statement_fingerprint,
  LEFT(s.query, ?) AS statement_preview,
  s.calls::bigint AS calls_total,
  s.total_exec_time::double precision AS total_exec_ms_total,
  s.rows::bigint AS rows_total
FROM pg_stat_statements s
JOIN pg_stat_statements_info i ON TRUE
JOIN pg_database d ON d.oid = s.dbid
WHERE s.query IS NOT NULL
ORDER BY s.total_exec_time DESC
LIMIT ?`, historyPreviewCharacters, sampler.config.MaxStatementsPerSample)
	if err != nil {
		return 0, safeHistoryError("read pg_stat_statements", err)
	}
	snapshots := make([]pgStatementSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, convertErr := statementSnapshotFromRow(row)
		if convertErr != nil {
			return 0, safeHistoryError("parse pg_stat_statements sample", convertErr)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := sampler.persistSnapshots(ctx, target, snapshots); err != nil {
		return len(snapshots), safeHistoryError("persist PostgreSQL history sample", err)
	}
	return len(snapshots), nil
}

func (sampler *HistorySampler) persistSnapshots(ctx context.Context, target HistoryTarget, snapshots []pgStatementSnapshot) error {
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var latest []struct {
			ObservedAt time.Time `json:"observed_at"`
		}
		if err := tx.Model("database_observation_checkpoints").Ctx(ctx).
			Fields("MAX(observed_at) AS observed_at").
			Where("target", target.Name).
			Where("kind", string(QuerySlowSQL)).
			Scan(&latest); err != nil {
			return err
		}

		for _, snapshot := range snapshots {
			var checkpoints []statementCheckpoint
			err := tx.Model("database_observation_checkpoints").Ctx(ctx).
				Fields("statement_fingerprint", "calls_total", "total_exec_ms_total", "rows_total", "observed_at", "reset_at").
				Where("target", target.Name).
				Where("kind", string(QuerySlowSQL)).
				Where("statement_fingerprint", snapshot.Fingerprint).
				Limit(1).
				Scan(&checkpoints)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			reset := len(checkpoints) == 0
			var previous statementCheckpoint
			if !reset {
				previous = checkpoints[0]
				maximumGap := sampler.config.SampleInterval * 2
				if maximumGap <= 0 {
					maximumGap = defaultHistorySampleInterval * 2
				}
				statsResetChanged := !snapshot.StatsReset.IsZero() && !previous.ResetAt.IsZero() && !snapshot.StatsReset.Equal(previous.ResetAt)
				reset = snapshot.Calls < previous.Calls || snapshot.TotalExecMS < previous.TotalExecMS || snapshot.Rows < previous.Rows ||
					!previous.ObservedAt.Before(snapshot.ObservedAt) || snapshot.ObservedAt.Sub(previous.ObservedAt) > maximumGap || statsResetChanged
			}
			if !reset {
				callsDelta := snapshot.Calls - previous.Calls
				totalExecMSDelta := snapshot.TotalExecMS - previous.TotalExecMS
				rowsDelta := snapshot.Rows - previous.Rows
				if callsDelta > 0 || totalExecMSDelta > 0 || rowsDelta > 0 {
					_, err = tx.Model("database_observation_samples").Ctx(ctx).Data(g.Map{
						"target":                target.Name,
						"engine":                string(target.Engine),
						"kind":                  string(QuerySlowSQL),
						"statement_fingerprint": snapshot.Fingerprint,
						"database_name":         snapshot.Database,
						"statement_preview":     snapshot.Preview,
						"bucket_start":          previous.ObservedAt.UTC(),
						"bucket_end":            snapshot.ObservedAt.UTC(),
						"calls_delta":           callsDelta,
						"total_exec_ms_delta":   totalExecMSDelta,
						"rows_delta":            rowsDelta,
						"time_semantics":        "sampled_delta",
					}).InsertIgnore()
					if err != nil {
						return err
					}
				}
			}

			resetAt := any(nil)
			if !snapshot.StatsReset.IsZero() {
				resetAt = snapshot.StatsReset.UTC()
			} else if reset && len(checkpoints) > 0 {
				resetAt = snapshot.ObservedAt.UTC()
			} else if !previous.ResetAt.IsZero() {
				resetAt = previous.ResetAt.UTC()
			}
			_, err = tx.Exec(`INSERT INTO database_observation_checkpoints(
target, kind, statement_fingerprint, database_name, statement_preview, calls_total, total_exec_ms_total, rows_total, observed_at, reset_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(target, kind, statement_fingerprint) DO UPDATE SET
database_name=excluded.database_name, statement_preview=excluded.statement_preview, calls_total=excluded.calls_total,
total_exec_ms_total=excluded.total_exec_ms_total, rows_total=excluded.rows_total, observed_at=excluded.observed_at,
reset_at=excluded.reset_at, updated_at=CURRENT_TIMESTAMP`,
				target.Name, string(QuerySlowSQL), snapshot.Fingerprint, snapshot.Database, snapshot.Preview,
				snapshot.Calls, snapshot.TotalExecMS, snapshot.Rows, snapshot.ObservedAt.UTC(), resetAt)
			if err != nil {
				return err
			}
		}
		if len(snapshots) > 0 && len(latest) > 0 && !latest[0].ObservedAt.IsZero() {
			intervalStart := latest[0].ObservedAt.UTC()
			intervalEnd := snapshots[0].ObservedAt.UTC()
			maximumGap := sampler.config.SampleInterval * 2
			if maximumGap <= 0 {
				maximumGap = defaultHistorySampleInterval * 2
			}
			if intervalStart.Before(intervalEnd) && intervalEnd.Sub(intervalStart) <= maximumGap {
				_, err := tx.Model("database_observation_intervals").Ctx(ctx).Data(g.Map{
					"target":       target.Name,
					"engine":       string(target.Engine),
					"bucket_start": intervalStart,
					"bucket_end":   intervalEnd,
				}).InsertIgnore()
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (sampler *HistorySampler) cleanup(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-sampler.config.Retention)
	if _, err := g.DB().Exec(ctx, "DELETE FROM database_observation_samples WHERE bucket_end < ?", cutoff); err != nil {
		return fmt.Errorf("cleanup database observation history: %w", err)
	}
	if _, err := g.DB().Exec(ctx, "DELETE FROM database_observation_intervals WHERE bucket_end < ?", cutoff); err != nil {
		return fmt.Errorf("cleanup database observation intervals: %w", err)
	}
	return nil
}

func statementSnapshotFromRow(row map[string]any) (pgStatementSnapshot, error) {
	fingerprint := strings.TrimSpace(fmt.Sprint(row["statement_fingerprint"]))
	if fingerprint == "" || fingerprint == "<nil>" {
		return pgStatementSnapshot{}, fmt.Errorf("pg_stat_statements returned an empty fingerprint")
	}
	observedAt, err := valueAsTime(row["observed_at"])
	if err != nil {
		return pgStatementSnapshot{}, err
	}
	database := boundedText(fmt.Sprint(row["database_name"]), 128)
	preview := boundedText(fmt.Sprint(row["statement_preview"]), historyPreviewCharacters)
	statsReset := time.Time{}
	if row["stats_reset"] != nil {
		statsReset, _ = valueAsTime(row["stats_reset"])
	}
	calls, err := valueAsInt64(row["calls_total"])
	if err != nil {
		return pgStatementSnapshot{}, fmt.Errorf("pg_stat_statements returned invalid calls: %w", err)
	}
	totalExecMS, err := valueAsFloat64(row["total_exec_ms_total"])
	if err != nil {
		return pgStatementSnapshot{}, fmt.Errorf("pg_stat_statements returned invalid execution time: %w", err)
	}
	rows, err := valueAsInt64(row["rows_total"])
	if err != nil {
		return pgStatementSnapshot{}, fmt.Errorf("pg_stat_statements returned invalid rows: %w", err)
	}
	return pgStatementSnapshot{
		Fingerprint: boundedText(database+":"+fingerprint, 256),
		Database:    database,
		Preview:     preview,
		Calls:       calls,
		TotalExecMS: totalExecMS,
		Rows:        rows,
		ObservedAt:  observedAt.UTC(),
		StatsReset:  statsReset.UTC(),
	}, nil
}

func valueAsTime(value any) (time.Time, error) {
	if converted, ok := value.(time.Time); ok && !converted.IsZero() {
		return converted, nil
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05"} {
		if converted, err := time.Parse(layout, text); err == nil {
			return converted, nil
		}
	}
	return time.Time{}, fmt.Errorf("database returned an invalid sample timestamp")
}

func valueAsInt64(value any) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
}

func valueAsFloat64(value any) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
}

func boundedText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	characters := []rune(value)
	if len(characters) > maximum {
		characters = characters[:maximum]
	}
	return string(characters)
}

func optionalConfigInt(ctx context.Context, key string) int {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return 0
	}
	return value.Int()
}

func optionalConfigBool(ctx context.Context, key string) bool {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return false
	}
	return value.Bool()
}

func valueAsBool(value any) bool {
	converted, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	return converted
}
