package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

// UpdateKnowledgePolicy changes declarations without regenerating source text or vectors.
func UpdateKnowledgePolicy(ctx context.Context, source string, record documentpolicy.Record) (int, error) {
	validated, err := documentpolicy.FromMetadata(map[string]any{documentpolicy.MetadataKey: record})
	if err != nil {
		return 0, err
	}
	count := 0
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var rows []knowledgeRow
		if err := tx.GetScan(&rows, `SELECT id,metadata FROM knowledge_documents WHERE json_extract(metadata,'$._source')=?`, source); err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("knowledge source is not indexed: %s", source)
		}
		for _, row := range rows {
			var meta map[string]any
			if err := json.Unmarshal([]byte(row.Metadata), &meta); err != nil {
				return err
			}
			meta[documentpolicy.MetadataKey] = validated
			encoded, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			if _, err := tx.Model("knowledge_documents").Where("id", row.ID).Data(g.Map{"metadata": string(encoded)}).Update(); err != nil {
				return err
			}
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
