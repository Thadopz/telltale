package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Thadopz/telltale/internal/ai/lexical"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

func migrateKnowledgeSearch(ctx context.Context, db gdb.DB) error {
	return db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Exec(`ALTER TABLE knowledge_documents ADD COLUMN lexical_text TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumnError(err) {
			return err
		}
		if _, err := tx.Exec(`CREATE VIRTUAL TABLE knowledge_fts USING fts5(tokens, tokenize="unicode61 tokenchars '_'")`); err != nil {
			return fmt.Errorf("create FTS5 knowledge index: %w", err)
		}
		var rows []struct {
			ID       string
			Content  string
			Metadata string
		}
		if err := tx.Model("knowledge_documents").Fields("id", "content", "metadata").Scan(&rows); err != nil {
			return err
		}
		for _, row := range rows {
			var meta map[string]any
			if err := json.Unmarshal([]byte(row.Metadata), &meta); err != nil {
				return err
			}
			if _, err := tx.Model("knowledge_documents").Where("id", row.ID).Data(g.Map{"lexical_text": lexical.Text(row.Content, meta)}).Update(); err != nil {
				return err
			}
		}
		for _, statement := range []string{
			`INSERT INTO knowledge_fts(rowid,tokens) SELECT rowid,lexical_text FROM knowledge_documents`,
			`INSERT OR IGNORE INTO knowledge_index_revision(id,revision) VALUES(1,lower(hex(randomblob(16))))`,
			`CREATE TRIGGER knowledge_search_insert AFTER INSERT ON knowledge_documents BEGIN INSERT INTO knowledge_fts(rowid,tokens) VALUES(NEW.rowid,NEW.lexical_text); UPDATE knowledge_index_revision SET revision=lower(hex(randomblob(16))) WHERE id=1; END`,
			`CREATE TRIGGER knowledge_search_update AFTER UPDATE ON knowledge_documents BEGIN DELETE FROM knowledge_fts WHERE rowid=OLD.rowid; INSERT INTO knowledge_fts(rowid,tokens) VALUES(NEW.rowid,NEW.lexical_text); UPDATE knowledge_index_revision SET revision=lower(hex(randomblob(16))) WHERE id=1; END`,
			`CREATE TRIGGER knowledge_search_delete AFTER DELETE ON knowledge_documents BEGIN DELETE FROM knowledge_fts WHERE rowid=OLD.rowid; UPDATE knowledge_index_revision SET revision=lower(hex(randomblob(16))) WHERE id=1; END`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
		_, err := tx.Model("schema_migrations").Data(g.Map{"version": 7, "applied_at": time.Now().UTC()}).Insert()
		return err
	})
}
