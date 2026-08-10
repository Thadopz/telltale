package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

type knowledgeRow struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Metadata string `json:"metadata"`
	Vector   string `json:"vector"`
}

type durableMemoryRow struct {
	MemoryID  int64  `json:"memory_id"`
	UserID    string `json:"user_id"`
	MemoryKey string `json:"memory_key"`
	Vector    string `json:"vector"`
	Status    string `json:"status"`
}

// UpsertKnowledgeDocuments stores document content, metadata and its
// embedding in SQLite. Similarity search is performed in-process, so no
// vector service is required for the standalone runtime.
func UpsertKnowledgeDocuments(ctx context.Context, ids []string, contents []string, metadata []string, vectors [][]float32) error {
	if len(ids) != len(contents) || len(ids) != len(metadata) || len(ids) != len(vectors) {
		return fmt.Errorf("knowledge vector data length mismatch")
	}
	for index, id := range ids {
		vectorJSON, err := json.Marshal(vectors[index])
		if err != nil {
			return fmt.Errorf("encode knowledge vector %s: %w", id, err)
		}
		data := g.Map{
			"id":         id,
			"content":    contents[index],
			"metadata":   metadata[index],
			"vector":     string(vectorJSON),
			"updated_at": time.Now(),
		}
		if _, err = g.DB().Model("knowledge_documents").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
			return err
		}
		if _, err = g.DB().Model("knowledge_documents").Ctx(ctx).Where("id", id).Data(data).Update(); err != nil {
			return err
		}
	}
	return nil
}

func DeleteKnowledgeBySource(ctx context.Context, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	var rows []knowledgeRow
	if err := g.DB().Model("knowledge_documents").Ctx(ctx).Fields("id", "metadata").Scan(&rows); err != nil {
		return err
	}
	for _, row := range rows {
		var metadata map[string]any
		if json.Unmarshal([]byte(row.Metadata), &metadata) != nil {
			continue
		}
		if fmt.Sprint(metadata["_source"]) != source {
			continue
		}
		if _, err := g.DB().Model("knowledge_documents").Ctx(ctx).Where("id", row.ID).Delete(); err != nil {
			return err
		}
	}
	return nil
}

func SearchKnowledge(ctx context.Context, queryVector []float32, topK int) ([]*schema.Document, error) {
	if len(queryVector) == 0 {
		return nil, fmt.Errorf("knowledge query vector is empty")
	}
	if topK <= 0 {
		topK = 12
	}

	var rows []knowledgeRow
	if err := g.DB().Model("knowledge_documents").Ctx(ctx).Fields("id", "content", "metadata", "vector").Scan(&rows); err != nil {
		return nil, err
	}

	type scoredDocument struct {
		document *schema.Document
		score    float64
	}
	scored := make([]scoredDocument, 0, len(rows))
	for _, row := range rows {
		var vector []float32
		if json.Unmarshal([]byte(row.Vector), &vector) != nil {
			continue
		}
		score, ok := cosineSimilarity(queryVector, vector)
		if !ok {
			continue
		}
		metadata := make(map[string]any)
		if strings.TrimSpace(row.Metadata) != "" {
			if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
				metadata = make(map[string]any)
			}
		}
		metadata["_retrieval"] = "sqlite_cosine"
		metadata["_vector_score"] = score
		scored = append(scored, scoredDocument{
			document: (&schema.Document{ID: row.ID, Content: row.Content, MetaData: metadata}).WithScore(score),
			score:    score,
		})
	}
	sort.SliceStable(scored, func(left, right int) bool {
		return scored[left].score > scored[right].score
	})
	if len(scored) > topK {
		scored = scored[:topK]
	}
	documents := make([]*schema.Document, 0, len(scored))
	for _, item := range scored {
		documents = append(documents, item.document)
	}
	return documents, nil
}

func UpsertDurableMemory(ctx context.Context, memoryID int64, userID string, memoryKey string, content string, vector []float32) error {
	vectorJSON, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	data := g.Map{
		"memory_id":  memoryID,
		"user_id":    strings.TrimSpace(userID),
		"memory_key": strings.TrimSpace(memoryKey),
		"content":    content,
		"status":     "active",
		"vector":     string(vectorJSON),
		"updated_at": time.Now(),
	}
	if _, err = g.DB().Model("durable_memory_vectors").Ctx(ctx).Data(data).InsertIgnore(); err != nil {
		return err
	}
	_, err = g.DB().Model("durable_memory_vectors").Ctx(ctx).Where("memory_id", memoryID).Data(data).Update()
	return err
}

func DeleteDurableMemory(ctx context.Context, memoryID int64) error {
	_, err := g.DB().Model("durable_memory_vectors").Ctx(ctx).Where("memory_id", memoryID).Delete()
	return err
}

func SearchDurableMemory(ctx context.Context, userID string, memoryKey string, queryVector []float32) (float64, bool, error) {
	var rows []durableMemoryRow
	err := g.DB().Model("durable_memory_vectors").Ctx(ctx).
		Fields("memory_id", "user_id", "memory_key", "status", "vector").
		Where("user_id", strings.TrimSpace(userID)).
		Where("memory_key", strings.TrimSpace(memoryKey)).
		Where("status", "active").
		Scan(&rows)
	if err != nil {
		return 0, false, err
	}
	for _, row := range rows {
		var vector []float32
		if json.Unmarshal([]byte(row.Vector), &vector) != nil {
			continue
		}
		if score, ok := cosineSimilarity(queryVector, vector); ok {
			return score, true, nil
		}
	}
	return 0, false, nil
}

func cosineSimilarity(left []float32, right []float32) (float64, bool) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, false
	}
	var dotProduct, leftLength, rightLength float64
	for index := range left {
		leftValue := float64(left[index])
		rightValue := float64(right[index])
		dotProduct += leftValue * rightValue
		leftLength += leftValue * leftValue
		rightLength += rightValue * rightValue
	}
	if leftLength == 0 || rightLength == 0 {
		return 0, false
	}
	return dotProduct / (math.Sqrt(leftLength) * math.Sqrt(rightLength)), true
}
