package storage

import (
	"path/filepath"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/lexical"
	"github.com/gogf/gf/v2/database/gdb"
)

func TestKnowledgeSearchMigrationBackfillsAndRollsBack(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		name := "valid"
		if invalid {
			name = "invalid-metadata"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			db, err := gdb.New(gdb.ConfigNode{Type: "sqlite", Link: "sqlite::@file(" + filepath.ToSlash(filepath.Join(t.TempDir(), "legacy.db")) + ")"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(ctx) })
			for _, sql := range []string{
				`CREATE TABLE knowledge_documents(id TEXT PRIMARY KEY,content TEXT,metadata TEXT,vector TEXT)`,
				`CREATE TABLE knowledge_index_revision(id INTEGER PRIMARY KEY,revision TEXT)`,
				`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at DATETIME)`,
				`INSERT INTO schema_migrations(version) VALUES(6)`,
			} {
				if _, err := db.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			meta := `{"_source":"old.md","_context_prefix":"数据库锁等待"}`
			if invalid {
				meta = "invalid-json"
			}
			if _, err := db.Exec(ctx, `INSERT INTO knowledge_documents VALUES(?,?,?,?)`, "old", "好友申请 ERR_409", meta, "[1,0]"); err != nil {
				t.Fatal(err)
			}
			err = applySchemaMigrations(ctx, db)
			if invalid {
				if err == nil {
					t.Fatal("invalid source migration accepted")
				}
				count, queryErr := db.GetCount(ctx, `SELECT count(*) FROM sqlite_master WHERE name='knowledge_fts'`)
				if queryErr != nil || count != 0 {
					t.Fatal("failed migration left a partial FTS index")
				}
				if _, err := db.Exec(ctx, `UPDATE knowledge_documents SET metadata=?`, `{"_source":"old.md"}`); err != nil {
					t.Fatal(err)
				}
				if err := applySchemaMigrations(ctx, db); err != nil {
					t.Fatalf("migration retry failed: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{"好友申请", "ERR_409", "锁等待"} {
					count, err := db.GetCount(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, lexical.Match(query))
					if err != nil || count != 1 {
						t.Fatalf("backfill failed for %s: %d %v", query, count, err)
					}
				}
			}
			if err := applySchemaMigrations(ctx, db); err != nil {
				t.Fatalf("migration not idempotent: %v", err)
			}
			value, err := db.GetValue(ctx, `SELECT vector FROM knowledge_documents WHERE id='old'`)
			if err != nil || value.String() != "[1,0]" {
				t.Fatal("migration changed existing embeddings")
			}
		})
	}
}
