package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		"detector_states",
		"incidents",
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
	if migrationCount != 3 {
		t.Fatalf("expected three schema migrations, got %d", migrationCount)
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
