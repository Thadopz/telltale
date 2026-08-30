package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

func TestInitializeCreatesStandaloneSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "telltale.db")
	t.Setenv("SUPERBIZ_SQLITE_PATH", databasePath)

	if err := Initialize(context.Background()); err != nil {
		t.Fatalf("initialize sqlite storage: %v", err)
	}
	defer g.DB().Close(context.Background())
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("expected sqlite database at %s: %v", databasePath, err)
	}
	var journalModes []struct {
		Mode string `json:"journal_mode"`
	}
	if err := g.DB().Raw("PRAGMA journal_mode").Scan(&journalModes); err != nil {
		t.Fatalf("read sqlite journal mode: %v", err)
	}
	if len(journalModes) != 1 || journalModes[0].Mode != "wal" {
		t.Fatalf("expected sqlite WAL mode, got %#v", journalModes)
	}

	var tables []struct {
		Name string `json:"name"`
	}
	if err := g.DB().Raw("SELECT name FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
		t.Fatalf("list sqlite tables: %v", err)
	}
	seen := make(map[string]bool, len(tables))
	for _, table := range tables {
		seen[table.Name] = true
	}
	for _, required := range []string{
		"schema_migrations",
		"retention_policy",
		"redaction_version",
		"conversations",
		"conversation_turns",
		"context_snapshots",
		"context_budget_calibration",
		"session_states",
		"tool_results",
		"pending_actions",
		"tool_operations",
		"knowledge_documents",
		"durable_memory_vectors",
		"profile_slots",
		"profile_candidates",
		"memory_events",
		"memory_buffers",
		"memory_forget_tombstones",
		"detector_states",
		"incidents",
		"incident_diagnosis_jobs",
		"database_observation_samples",
		"database_observation_intervals",
		"database_observation_checkpoints",
		"database_observation_leases",
		"database_observation_status",
	} {
		if !seen[required] {
			t.Fatalf("required standalone table %q was not created", required)
		}
	}
	if seen["customers"] || seen["customer_data"] {
		t.Fatal("standalone schema must not create customer tables")
	}

	migrationCount, err := g.DB().Model("schema_migrations").Count()
	if err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if migrationCount != 6 {
		t.Fatalf("expected six schema migrations, got %d", migrationCount)
	}
	metadataCount, err := g.DB().Model("retention_policy").Where("policy_key", "default").Count()
	if err != nil {
		t.Fatalf("read retention metadata: %v", err)
	}
	if metadataCount != 1 {
		t.Fatalf("expected one default retention policy, got %d", metadataCount)
	}
	redactionCount, err := g.DB().Model("redaction_version").Where("version", currentRedactionVersion).Count()
	if err != nil {
		t.Fatalf("read redaction metadata: %v", err)
	}
	if redactionCount != 1 {
		t.Fatalf("expected redaction version %d, got %d rows", currentRedactionVersion, redactionCount)
	}

	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if _, err := g.DB().Model("context_snapshots").Data(g.Map{
		"conversation_id": "retention-test",
		"summary":         "expired context",
		"facts_json":      "[]",
		"created_at":      old,
	}).Insert(); err != nil {
		t.Fatalf("insert expired snapshot: %v", err)
	}
	if _, err := CleanupExpiredData(context.Background()); err != nil {
		t.Fatalf("cleanup expired data: %v", err)
	}
	expiredCount, err := g.DB().Model("context_snapshots").Where("summary", "expired context").Count()
	if err != nil {
		t.Fatalf("check expired snapshot cleanup: %v", err)
	}
	if expiredCount != 0 {
		t.Fatalf("expected expired snapshot to be removed, got %d rows", expiredCount)
	}
}

func TestMigrationsFourThroughSixUpgradeExistingIncidentDatabase(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, err := gdb.New(gdb.ConfigNode{
		Type: "sqlite",
		Link: "sqlite::@file(" + filepath.ToSlash(databasePath) + ")",
	})
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer database.Close(ctx)

	statements := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (3, CURRENT_TIMESTAMP)`,
		`CREATE TABLE incidents (
			id TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL UNIQUE,
			rule_id TEXT NOT NULL,
			domain TEXT NOT NULL,
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
		`INSERT INTO incidents(id, fingerprint, rule_id, domain, status, severity, first_seen_at, last_seen_at, diagnosis)
		 VALUES ('inc_legacy', 'legacy', 'legacy-rule', 'observability', 'firing', 'critical', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'legacy diagnosis')`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare legacy database: %v", err)
		}
	}
	if err := applySchemaMigrations(ctx, database); err != nil {
		t.Fatalf("apply migrations four through six: %v", err)
	}

	var columns []struct {
		Name string `json:"name"`
	}
	if err := database.Raw("PRAGMA table_info(incidents)").Scan(&columns); err != nil {
		t.Fatalf("read incident columns: %v", err)
	}
	seenColumns := make(map[string]bool)
	for _, column := range columns {
		seenColumns[column.Name] = true
	}
	if !seenColumns["service"] || !seenColumns["environment"] {
		t.Fatalf("migration did not add incident scope columns: %#v", seenColumns)
	}
	jobTableCount, err := database.Model("sqlite_master").Where("type", "table").Where("name", "incident_diagnosis_jobs").Count()
	if err != nil || jobTableCount != 1 {
		t.Fatalf("migration did not create diagnosis job table: count=%d err=%v", jobTableCount, err)
	}
	for _, tableName := range []string{"database_observation_samples", "database_observation_intervals", "database_observation_checkpoints", "database_observation_leases", "database_observation_status"} {
		count, tableErr := database.Model("sqlite_master").Where("type", "table").Where("name", tableName).Count()
		if tableErr != nil || count != 1 {
			t.Fatalf("migration five did not create %s: count=%d err=%v", tableName, count, tableErr)
		}
	}
	for _, tableName := range []string{"profile_slots", "profile_candidates", "memory_events", "memory_buffers", "memory_forget_tombstones"} {
		count, tableErr := database.Model("sqlite_master").Where("type", "table").Where("name", tableName).Count()
		if tableErr != nil || count != 1 {
			t.Fatalf("migration six did not create %s: count=%d err=%v", tableName, count, tableErr)
		}
	}
	var legacy struct {
		Diagnosis string `json:"diagnosis"`
	}
	if err := database.Model("incidents").Where("id", "inc_legacy").Scan(&legacy); err != nil {
		t.Fatalf("read migrated incident: %v", err)
	}
	if legacy.Diagnosis != "legacy diagnosis" {
		t.Fatalf("migration lost existing diagnosis: %#v", legacy)
	}
}
