package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/Thadopz/telltale/internal/ai/lexical"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
)

type knowledgeRow struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Metadata string `json:"metadata"`
	Vector   string `json:"vector"`
}

func KnowledgeBySource(ctx context.Context, source string) ([]*schema.Document, error) {
	var rows []knowledgeRow
	if err := g.DB().Model("knowledge_documents").Ctx(ctx).Fields("id", "content", "metadata").Scan(&rows); err != nil {
		return nil, err
	}
	var docs []*schema.Document
	for _, row := range rows {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
			return nil, fmt.Errorf("decode knowledge metadata %s: %w", row.ID, err)
		}
		if metadata["_source"] == source {
			docs = append(docs, &schema.Document{ID: row.ID, Content: row.Content, MetaData: metadata})
		}
	}
	return docs, nil
}

// KnowledgeRevision also observes updates made by a separate CLI process.
func KnowledgeRevision(ctx context.Context) (string, error) {
	value, err := g.DB().Model("knowledge_index_revision").Ctx(ctx).Where("id", 1).Value("revision")
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// ReplaceKnowledgeSource performs no remote calls inside the transaction.
func ReplaceKnowledgeSource(ctx context.Context, source string, ids, contents, metadata []string, vectors [][]float32) error {
	if strings.TrimSpace(source) == "" || len(ids) != len(contents) || len(ids) != len(metadata) || len(ids) != len(vectors) {
		return fmt.Errorf("invalid knowledge replacement")
	}
	data := make([]g.Map, len(ids))
	seen := make(map[string]bool, len(ids))
	for index, id := range ids {
		var meta map[string]any
		if err := json.Unmarshal([]byte(metadata[index]), &meta); err != nil {
			return err
		}
		if meta["_source"] != source || id == "" || seen[id] || len(vectors[index]) == 0 {
			return fmt.Errorf("invalid knowledge replacement document %q", id)
		}
		if _, err := documentpolicy.FromMetadata(meta); err != nil {
			return err
		}
		seen[id] = true
		if _, err := unitVector(vectors[index]); err != nil {
			return fmt.Errorf("invalid knowledge vector %s: %w", id, err)
		}
		encoded, err := json.Marshal(vectors[index])
		if err != nil {
			return err
		}
		data[index] = g.Map{"id": id, "content": contents[index], "metadata": metadata[index], "vector": string(encoded), "lexical_text": lexical.Text(contents[index], meta)}
	}
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var rows []knowledgeRow
		if err := tx.Model("knowledge_documents").Ctx(ctx).Fields("id", "metadata").Scan(&rows); err != nil {
			return err
		}
		for _, row := range rows {
			var meta map[string]any
			if err := json.Unmarshal([]byte(row.Metadata), &meta); err != nil {
				return err
			}
			if meta["_source"] == source {
				if _, err := tx.Model("knowledge_documents").Ctx(ctx).Where("id", row.ID).Delete(); err != nil {
					return err
				}
			}
		}
		for _, item := range data {
			item["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := tx.Model("knowledge_documents").Ctx(ctx).Data(item).Insert(); err != nil {
				return err
			}
		}
		if _, err := tx.Model("knowledge_index_revision").Ctx(ctx).Data(g.Map{"id": 1, "revision": ""}).InsertIgnore(); err != nil {
			return err
		}
		_, err := tx.Model("knowledge_index_revision").Ctx(ctx).Where("id", 1).Data(g.Map{"revision": uuid.NewString()}).Update()
		return err
	})
}

// UpsertKnowledgeDocuments stores document content, metadata and its
// embedding in SQLite. Similarity search is performed in-process, so no
// vector service is required for the standalone runtime.
func UpsertKnowledgeDocuments(ctx context.Context, ids []string, contents []string, metadata []string, vectors [][]float32) error {
	if len(ids) != len(contents) || len(ids) != len(metadata) || len(ids) != len(vectors) {
		return fmt.Errorf("knowledge vector data length mismatch")
	}
	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		for index, id := range ids {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("knowledge document id is empty")
			}
			if _, err := unitVector(vectors[index]); err != nil {
				return fmt.Errorf("invalid knowledge vector %s: %w", id, err)
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(metadata[index]), &meta); err != nil {
				return err
			}
			if _, err := documentpolicy.FromMetadata(meta); err != nil {
				return err
			}
			vectorJSON, err := json.Marshal(vectors[index])
			if err != nil {
				return fmt.Errorf("encode knowledge vector %s: %w", id, err)
			}
			data := g.Map{
				"id":           id,
				"content":      contents[index],
				"metadata":     metadata[index],
				"vector":       string(vectorJSON),
				"lexical_text": lexical.Text(contents[index], meta),
				"updated_at":   time.Now(),
			}
			if _, err = tx.Model("knowledge_documents").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
				return err
			}
			if _, err = tx.Model("knowledge_documents").Ctx(ctx).Where("id", id).Data(data).Update(); err != nil {
				return err
			}
		}
		return nil
	})
}

func DeleteKnowledgeBySource(ctx context.Context, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	return ReplaceKnowledgeSource(ctx, source, nil, nil, nil, nil)
}

func SearchKnowledge(ctx context.Context, queryVector []float32, topK int) ([]*schema.Document, error) {
	if topK <= 0 {
		topK = 12
	}
	rows, _, err := loadSearchSnapshot(ctx, "", topK)
	if err != nil {
		return nil, err
	}
	scored, err := denseRanks(rows, queryVector, topK)
	if err != nil {
		return nil, err
	}
	documents := make([]*schema.Document, 0, len(scored))
	for _, item := range scored {
		doc := cloneSearchDocument(item.doc)
		doc.MetaData["_retrieval"] = "sqlite_cosine"
		doc.MetaData["_vector_score"] = item.score
		documents = append(documents, doc.WithScore(item.score))
	}
	return documents, nil
}
