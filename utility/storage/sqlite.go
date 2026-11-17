package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/sqlite/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

const defaultSQLitePath = "data/telltale.db"

const currentRedactionVersion = 1

var initializeOnce sync.Once
var initializeErr error

// Initialize configures the default GoFrame database as a local SQLite file
// and creates only the tables used by the standalone agent.
func Initialize(ctx context.Context) error {
	initializeOnce.Do(func() {
		initializeErr = initialize(ctx)
	})
	return initializeErr
}

func initialize(ctx context.Context) error {
	path := sqlitePath(ctx)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create sqlite directory: %w", err)
	}

	gdb.SetConfig(gdb.Config{
		gdb.DefaultGroupName: {
			{
				Type:                 "sqlite",
				Link:                 "sqlite::@file(" + filepath.ToSlash(path) + ")",
				MaxOpenConnCount:     1,
				MaxIdleConnCount:     1,
				TimeMaintainDisabled: true,
			},
		},
	})

	db := g.DB()
	for _, statement := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL"} {
		if _, err := db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite concurrency: %w", err)
		}
	}
	for _, statement := range schemaStatements() {
		if _, err := db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite schema: %w", err)
		}
	}
	if err := applySchemaMigrations(ctx, db); err != nil {
		return err
	}
	if err := ensureStorageMetadata(ctx, db); err != nil {
		return err
	}
	return nil
}

func ensureStorageMetadata(ctx context.Context, db gdb.DB) error {
	retentionHours := 24 * 7
	if value, err := g.Cfg().Get(ctx, "storage.retention_hours"); err == nil && value != nil && value.Int() > 0 {
		retentionHours = value.Int()
	}
	if _, err := db.Model("retention_policy").Ctx(ctx).Data(g.Map{
		"policy_key":    "default",
		"max_age_hours": retentionHours,
		"max_rows":      0,
		"updated_at":    time.Now().UTC(),
	}).InsertIgnore(); err != nil {
		return fmt.Errorf("record sqlite retention policy: %w", err)
	}
	if _, err := db.Model("redaction_version").Ctx(ctx).Data(g.Map{
		"version":    currentRedactionVersion,
		"applied_at": time.Now().UTC(),
	}).InsertIgnore(); err != nil {
		return fmt.Errorf("record sqlite redaction version: %w", err)
	}
	return nil
}

func applySchemaMigrations(ctx context.Context, db gdb.DB) error {
	var rows []struct {
		Version int `json:"version"`
	}
	if err := db.Model("schema_migrations").Ctx(ctx).Fields("version").OrderDesc("version").Limit(1).Scan(&rows); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	current := 0
	if len(rows) > 0 {
		current = rows[0].Version
	}
	migrations := map[int][]string{
		1: {
			`ALTER TABLE tool_results ADD COLUMN issuer TEXT`,
			`ALTER TABLE tool_results ADD COLUMN run_id TEXT`,
			`ALTER TABLE tool_results ADD COLUMN tenant_id TEXT`,
			`ALTER TABLE tool_results ADD COLUMN observed_at DATETIME`,
			`ALTER TABLE tool_results ADD COLUMN expires_at DATETIME`,
			`ALTER TABLE tool_results ADD COLUMN content_digest TEXT`,
			`ALTER TABLE tool_results ADD COLUMN trust_level TEXT`,
			`ALTER TABLE tool_results ADD COLUMN operation_id TEXT`,
		},
		2: {
			`CREATE INDEX IF NOT EXISTS idx_tool_results_evidence_id ON tool_results(evidence_id)`,
			`CREATE INDEX IF NOT EXISTS idx_tool_results_run_id ON tool_results(run_id)`,
			`CREATE INDEX IF NOT EXISTS idx_tool_results_expires_at ON tool_results(expires_at)`,
		},
		3: {
			`ALTER TABLE pending_actions ADD COLUMN operation_id TEXT`,
			`CREATE INDEX IF NOT EXISTS idx_pending_actions_operation_id ON pending_actions(operation_id)`,
		},
		4: diagnosisJobSchemaStatements(),
		5: databaseObservationSchemaStatements(),
		// Replaying the idempotent standalone schema lets an older database
		// receive every Profile/Event/Buffer table without destructive ALTERs.
		6: schemaStatements(),
	}
	for version := current + 1; version <= 6; version++ {
		for _, statement := range migrations[version] {
			if _, err := db.Exec(ctx, statement); err != nil && !isDuplicateColumnError(err) {
				return fmt.Errorf("apply sqlite migration %d: %w", version, err)
			}
		}
		if _, err := db.Model("schema_migrations").Ctx(ctx).Data(g.Map{
			"version":    version,
			"applied_at": time.Now().UTC(),
		}).Insert(); err != nil {
			return fmt.Errorf("record sqlite migration %d: %w", version, err)
		}
	}
	if current < 7 {
		return migrateKnowledgeSearch(ctx, db)
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists")
}

// CleanupExpiredData removes only data covered by the local retention policy.
// It is explicit so an operator can schedule it without coupling cleanup to
// every database initialization in tests or short-lived CLI commands.
func CleanupExpiredData(ctx context.Context) (int64, error) {
	retentionHours := 24 * 7
	if value, err := g.Cfg().Get(ctx, "storage.retention_hours"); err == nil && value != nil && value.Int() > 0 {
		retentionHours = value.Int()
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionHours) * time.Hour)
	total := int64(0)
	for _, table := range []string{"context_snapshots", "tool_results", "conversation_turns"} {
		result, err := g.DB().Exec(ctx, "DELETE FROM "+table+" WHERE created_at < ?", cutoff)
		if err != nil {
			return total, fmt.Errorf("cleanup sqlite table %s: %w", table, err)
		}
		if affected, err := result.RowsAffected(); err == nil {
			total += affected
		}
	}
	return total, nil
}

func sqlitePath(ctx context.Context) string {
	if value := strings.TrimSpace(os.Getenv("SUPERBIZ_SQLITE_PATH")); value != "" {
		return value
	}
	if value, err := g.Cfg().Get(ctx, "storage.sqlite_path"); err == nil && value != nil {
		if path := strings.TrimSpace(value.String()); path != "" {
			return path
		}
	}
	return defaultSQLitePath
}

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS retention_policy (
			policy_key TEXT PRIMARY KEY,
			max_age_hours INTEGER NOT NULL,
			max_rows INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS redaction_version (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL UNIQUE,
			user_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_user_updated ON conversations(user_id, updated_at)`,
		`CREATE TABLE IF NOT EXISTS conversation_turns (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_turns_conversation_created ON conversation_turns(conversation_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS conversation_summaries (
			conversation_id TEXT PRIMARY KEY,
			summary TEXT NOT NULL,
			covers_through_turn_id INTEGER,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS context_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '',
			facts_json TEXT NOT NULL DEFAULT '[]',
			generation INTEGER NOT NULL DEFAULT 0,
			before_tokens INTEGER NOT NULL DEFAULT 0,
			after_tokens INTEGER NOT NULL DEFAULT 0,
			compacted_messages INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_snapshots_conversation_created ON context_snapshots(conversation_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS context_budget_calibration (
			model_key TEXT PRIMARY KEY,
			estimate_factor REAL NOT NULL DEFAULT 1.0,
			sample_count INTEGER NOT NULL DEFAULT 0,
			last_prompt_tokens INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS session_states (
			conversation_id TEXT PRIMARY KEY,
			goal TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			state TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tool_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			input TEXT,
			output_summary TEXT NOT NULL,
			output TEXT,
			status TEXT NOT NULL,
			evidence_id TEXT,
			failed_query_id TEXT,
			can_use_as_evidence INTEGER NOT NULL DEFAULT 0,
			data_status TEXT NOT NULL DEFAULT 'unknown',
			subject TEXT,
			scope TEXT,
			observations TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_results_conversation_created ON tool_results(conversation_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS pending_actions (
			token TEXT PRIMARY KEY,
			tool_name TEXT NOT NULL,
			operation_id TEXT,
			arguments_hash TEXT NOT NULL,
			risk TEXT NOT NULL,
			requires_confirm INTEGER NOT NULL DEFAULT 1,
			expires_at DATETIME NOT NULL,
			user_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			roles_key TEXT NOT NULL,
			arguments_json TEXT NOT NULL,
			confirmed INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tool_operations (
			operation_id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			status TEXT NOT NULL,
			result TEXT,
			evidence_id TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_operations_conversation_updated ON tool_operations(conversation_id, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_actions_expires_at ON pending_actions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_actions_operation_id ON pending_actions(operation_id)`,
		`CREATE TABLE IF NOT EXISTS knowledge_documents (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			vector TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_documents_updated ON knowledge_documents(updated_at)`,
		`CREATE TABLE IF NOT EXISTS knowledge_index_revision (id INTEGER PRIMARY KEY CHECK(id = 1), revision TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS profile_slots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			slot_key TEXT NOT NULL,
			value_type TEXT NOT NULL,
			value_json TEXT NOT NULL,
			value_text TEXT NOT NULL,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			confirmation TEXT NOT NULL,
			confidence REAL NOT NULL,
			source_conversation_id TEXT NOT NULL DEFAULT '',
			source_turn_ids TEXT NOT NULL DEFAULT '[]',
			source_excerpt TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
			valid_from DATETIME NOT NULL,
			valid_until DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, user_id, slot_key, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_slots_scope_status ON profile_slots(tenant_id, user_id, status, slot_key)`,
		`CREATE TABLE IF NOT EXISTS profile_candidates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			slot_key TEXT NOT NULL,
			value_type TEXT NOT NULL,
			value_json TEXT NOT NULL,
			value_text TEXT NOT NULL,
			revision INTEGER NOT NULL,
			confidence REAL NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			source_conversation_id TEXT NOT NULL DEFAULT '',
			source_turn_ids TEXT NOT NULL DEFAULT '[]',
			source_excerpt TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, user_id, slot_key, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_candidates_scope_status ON profile_candidates(tenant_id, user_id, status, expires_at)`,
		`CREATE TABLE IF NOT EXISTS memory_events (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			incident_id TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			tags_json TEXT NOT NULL DEFAULT '[]',
			entities_json TEXT NOT NULL DEFAULT '[]',
			source_turn_ids TEXT NOT NULL DEFAULT '[]',
			source_hash TEXT NOT NULL,
			time_expression TEXT NOT NULL DEFAULT '',
			time_confidence TEXT NOT NULL DEFAULT 'unknown',
			occurred_at DATETIME,
			recorded_at DATETIME NOT NULL,
			profile_delta_ids TEXT NOT NULL DEFAULT '[]',
			evidence_ids TEXT NOT NULL DEFAULT '[]',
			evidence_status TEXT NOT NULL DEFAULT 'unverified',
			status TEXT NOT NULL,
			vector_status TEXT NOT NULL DEFAULT 'pending',
			vector TEXT NOT NULL DEFAULT '',
			mention_count INTEGER NOT NULL DEFAULT 1,
			extractor_version TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, user_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_events_scope_time ON memory_events(tenant_id, user_id, status, recorded_at)`,
		`CREATE TABLE IF NOT EXISTS memory_buffers (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			conversation_storage_id TEXT NOT NULL,
			pending_turn_ids TEXT NOT NULL DEFAULT '[]',
			claimed_turn_ids TEXT NOT NULL DEFAULT '[]',
			pending_tokens INTEGER NOT NULL DEFAULT 0,
			claimed_tokens INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			explicit_flush INTEGER NOT NULL DEFAULT 0,
			generation INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			lease_until DATETIME,
			last_activity_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, user_id, conversation_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_buffers_ready ON memory_buffers(status, next_attempt_at, last_activity_at)`,
		`CREATE TABLE IF NOT EXISTS memory_forget_tombstones (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			scope_kind TEXT NOT NULL,
			scope_value TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_tombstones_scope ON memory_forget_tombstones(tenant_id, user_id, generation)`,
		`CREATE TABLE IF NOT EXISTS detector_states (
			state_key TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'normal',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			consecutive_successes INTEGER NOT NULL DEFAULT 0,
			last_value REAL NOT NULL DEFAULT 0,
			last_evidence_id TEXT,
			last_error TEXT,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL UNIQUE,
			rule_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			service TEXT NOT NULL DEFAULT '',
			environment TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			severity TEXT NOT NULL,
			first_seen_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL,
			resolved_at DATETIME,
			latest_value REAL NOT NULL DEFAULT 0,
			evidence_id TEXT,
			evidence_json TEXT,
			diagnosis TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		diagnosisJobTableStatement(),
		`CREATE INDEX IF NOT EXISTS idx_incident_diagnosis_jobs_ready ON incident_diagnosis_jobs(status, next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS idx_incident_diagnosis_jobs_incident ON incident_diagnosis_jobs(incident_id, episode_opened_at DESC)`,
		databaseObservationSampleTableStatement(),
		databaseObservationIntervalTableStatement(),
		databaseObservationCheckpointTableStatement(),
		databaseObservationLeaseTableStatement(),
		databaseObservationStatusTableStatement(),
		`CREATE INDEX IF NOT EXISTS idx_database_observation_samples_range ON database_observation_samples(target, kind, bucket_start, bucket_end)`,
		`CREATE INDEX IF NOT EXISTS idx_database_observation_intervals_range ON database_observation_intervals(target, bucket_start, bucket_end)`,
	}
}

func databaseObservationSchemaStatements() []string {
	return []string{
		databaseObservationSampleTableStatement(),
		databaseObservationIntervalTableStatement(),
		databaseObservationCheckpointTableStatement(),
		databaseObservationLeaseTableStatement(),
		databaseObservationStatusTableStatement(),
		`CREATE INDEX IF NOT EXISTS idx_database_observation_samples_range ON database_observation_samples(target, kind, bucket_start, bucket_end)`,
		`CREATE INDEX IF NOT EXISTS idx_database_observation_intervals_range ON database_observation_intervals(target, bucket_start, bucket_end)`,
	}
}

func databaseObservationSampleTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS database_observation_samples (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target TEXT NOT NULL,
		engine TEXT NOT NULL,
		kind TEXT NOT NULL,
		statement_fingerprint TEXT NOT NULL,
		database_name TEXT NOT NULL DEFAULT '',
		statement_preview TEXT NOT NULL DEFAULT '',
		bucket_start DATETIME NOT NULL,
		bucket_end DATETIME NOT NULL,
		calls_delta INTEGER NOT NULL DEFAULT 0,
		total_exec_ms_delta REAL NOT NULL DEFAULT 0,
		rows_delta INTEGER NOT NULL DEFAULT 0,
		time_semantics TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(target, kind, statement_fingerprint, bucket_end)
	)`
}

func databaseObservationIntervalTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS database_observation_intervals (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target TEXT NOT NULL,
		engine TEXT NOT NULL,
		bucket_start DATETIME NOT NULL,
		bucket_end DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(target, bucket_end)
	)`
}

func databaseObservationCheckpointTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS database_observation_checkpoints (
		target TEXT NOT NULL,
		kind TEXT NOT NULL,
		statement_fingerprint TEXT NOT NULL,
		database_name TEXT NOT NULL DEFAULT '',
		statement_preview TEXT NOT NULL DEFAULT '',
		calls_total INTEGER NOT NULL DEFAULT 0,
		total_exec_ms_total REAL NOT NULL DEFAULT 0,
		rows_total INTEGER NOT NULL DEFAULT 0,
		observed_at DATETIME NOT NULL,
		reset_at DATETIME,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(target, kind, statement_fingerprint)
	)`
}

func databaseObservationLeaseTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS database_observation_leases (
		target TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		lease_until DATETIME NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
}

func databaseObservationStatusTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS database_observation_status (
		target TEXT PRIMARY KEY,
		engine TEXT NOT NULL,
		status TEXT NOT NULL,
		error_code TEXT,
		last_error TEXT,
		last_success_at DATETIME,
		last_attempt_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
}

func diagnosisJobSchemaStatements() []string {
	return []string{
		`ALTER TABLE incidents ADD COLUMN service TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE incidents ADD COLUMN environment TEXT NOT NULL DEFAULT ''`,
		diagnosisJobTableStatement(),
		`CREATE INDEX IF NOT EXISTS idx_incident_diagnosis_jobs_ready ON incident_diagnosis_jobs(status, next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS idx_incident_diagnosis_jobs_incident ON incident_diagnosis_jobs(incident_id, episode_opened_at DESC)`,
	}
}

func diagnosisJobTableStatement() string {
	return `CREATE TABLE IF NOT EXISTS incident_diagnosis_jobs (
		id TEXT PRIMARY KEY,
		incident_id TEXT NOT NULL,
		episode_opened_at DATETIME NOT NULL,
		request_json TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		attempt_count INTEGER NOT NULL DEFAULT 0,
		next_attempt_at DATETIME NOT NULL,
		lease_until DATETIME,
		conversation_id TEXT NOT NULL,
		run_id TEXT,
		result_json TEXT,
		last_error TEXT,
		started_at DATETIME,
		completed_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(incident_id, episode_opened_at)
	)`
}
