package vectorstore

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"

	"github.com/Thadopz/telltale/internal/ai/lexical"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

type vectorDocument struct {
	doc    *schema.Document
	vector []float32
	policy documentpolicy.Record
}
type keywordRank struct {
	ID    string
	Score float64
}
type rankedDocument struct {
	doc   *schema.Document
	score float64
}

var knowledgeSnapshot struct {
	sync.Mutex
	revision  string
	database  string
	documents []vectorDocument
	loaded    bool
}

func loadSearchSnapshot(ctx context.Context, query string, topK int) ([]vectorDocument, []keywordRank, error) {
	knowledgeSnapshot.Lock()
	defer knowledgeSnapshot.Unlock()
	var documents []vectorDocument
	var keywords []keywordRank
	var revision string
	database := g.DB().GetConfig().Link
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		value, err := tx.Model("knowledge_index_revision").Where("id", 1).Value("revision")
		if err != nil {
			return err
		}
		revision = value.String()
		if knowledgeSnapshot.loaded && revision == knowledgeSnapshot.revision && database == knowledgeSnapshot.database {
			documents = knowledgeSnapshot.documents
		} else {
			var rows []knowledgeRow
			if err := tx.Model("knowledge_documents").Fields("id", "content", "metadata", "vector").OrderAsc("id").Scan(&rows); err != nil {
				return err
			}
			for _, row := range rows {
				var vector []float32
				if err := json.Unmarshal([]byte(row.Vector), &vector); err != nil {
					return fmt.Errorf("decode knowledge vector %s: %w", row.ID, err)
				}
				unit, err := unitVector(vector)
				if err != nil {
					return fmt.Errorf("knowledge vector %s: %w", row.ID, err)
				}
				var metadata map[string]any
				if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
					return fmt.Errorf("decode knowledge metadata %s: %w", row.ID, err)
				}
				if metadata == nil {
					metadata = map[string]any{}
				}
				policy, err := documentpolicy.FromMetadata(metadata)
				if err != nil {
					return fmt.Errorf("document policy %s: %w", row.ID, err)
				}
				documents = append(documents, vectorDocument{doc: &schema.Document{ID: row.ID, Content: row.Content, MetaData: metadata}, vector: unit, policy: policy})
			}
		}
		if match := lexical.Match(query); match != "" {
			filter := documentpolicy.FromContext(ctx)
			allowed := map[string]bool{}
			for _, item := range documents {
				allowed[item.doc.ID] = item.policy.Allows(filter)
			}
			// Filter before the candidate cap; old high-ranking documents must not crowd out valid sources.
			for offset := 0; len(keywords) < topK; offset += 64 {
				var page []keywordRank
				if err := tx.GetScan(&page, `SELECT d.id,bm25(knowledge_fts) AS score FROM knowledge_fts JOIN knowledge_documents d ON d.rowid=knowledge_fts.rowid WHERE knowledge_fts MATCH ? ORDER BY score ASC,d.id ASC LIMIT 64 OFFSET ?`, match, offset); err != nil {
					return err
				}
				for _, item := range page {
					if allowed[item.ID] {
						keywords = append(keywords, item)
						if len(keywords) == topK {
							break
						}
					}
				}
				if len(page) < 64 {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	knowledgeSnapshot.revision, knowledgeSnapshot.documents, knowledgeSnapshot.loaded = revision, documents, true
	knowledgeSnapshot.database = database
	return documents, keywords, nil
}

func unitVector(vector []float32) ([]float32, error) {
	var norm float64
	for _, v := range vector {
		norm += float64(v) * float64(v)
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("empty, zero or non-finite vector")
	}
	norm = math.Sqrt(norm)
	unit := make([]float32, len(vector))
	for i, v := range vector {
		unit[i] = float32(float64(v) / norm)
	}
	return unit, nil
}

func denseRanks(documents []vectorDocument, query []float32, topK int) ([]rankedDocument, error) {
	unit, err := unitVector(query)
	if err != nil {
		return nil, err
	}
	ranks := make([]rankedDocument, 0, len(documents))
	for _, item := range documents {
		if len(item.vector) != len(unit) {
			return nil, fmt.Errorf("query and indexed vector dimensions differ for %s", item.doc.ID)
		}
		var score float64
		for i, v := range unit {
			score += float64(v) * float64(item.vector[i])
		}
		ranks = append(ranks, rankedDocument{doc: item.doc, score: score})
	}
	slices.SortFunc(ranks, func(a, b rankedDocument) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(a.doc.ID, b.doc.ID)
	})
	return ranks[:min(topK, len(ranks))], nil
}

// cloneSearchDocument keeps caller metadata mutations outside the shared snapshot.
func cloneSearchDocument(doc *schema.Document) *schema.Document {
	copy := *doc
	copy.MetaData = nil
	encoded, _ := json.Marshal(doc.MetaData)
	_ = json.Unmarshal(encoded, &copy.MetaData)
	return &copy
}

func SearchHybridKnowledge(ctx context.Context, query string, queryVector []float32, topK, rrfK int) ([]*schema.Document, error) {
	filter := documentpolicy.FromContext(ctx)
	ctx = documentpolicy.WithFilter(ctx, filter)
	if topK <= 0 {
		topK = 12
	}
	if rrfK <= 0 {
		return nil, fmt.Errorf("rrf_k must be positive")
	}
	documents, keywords, err := loadSearchSnapshot(ctx, query, topK)
	if err != nil {
		return nil, err
	}
	eligible := make([]vectorDocument, 0, len(documents))
	for _, item := range documents {
		if item.policy.Allows(filter) {
			eligible = append(eligible, item)
		}
	}
	documents = eligible
	dense, err := denseRanks(documents, queryVector, topK)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*schema.Document, len(documents))
	policies := map[string]documentpolicy.Record{}
	for _, item := range documents {
		byID[item.doc.ID] = item.doc
		policies[item.doc.ID] = item.policy
	}
	merged := map[string]*schema.Document{}
	add := func(id string, rank int, kind string, score float64) {
		doc := merged[id]
		if doc == nil {
			doc = cloneSearchDocument(byID[id])
			doc.MetaData = documentpolicy.Annotate(doc.MetaData, policies[id], filter)
			doc.MetaData["_retrieval"] = "sqlite_bm25_dense_rrf"
			doc = doc.WithScore(0)
			merged[id] = doc
		}
		doc.MetaData["_"+kind+"_rank"] = rank
		doc.MetaData["_"+kind+"_score"] = score
		doc.WithScore(doc.Score() + 1/(float64(rrfK)+float64(rank)))
	}
	for i, item := range dense {
		add(item.doc.ID, i+1, "vector", item.score)
	}
	for i, item := range keywords {
		add(item.ID, i+1, "keyword", item.Score)
	}
	result := make([]*schema.Document, 0, len(merged))
	for _, doc := range merged {
		doc.MetaData["_rrf_score"] = doc.Score()
		result = append(result, doc)
	}
	slices.SortFunc(result, func(a, b *schema.Document) int {
		if c := cmp.Compare(b.Score(), a.Score()); c != 0 {
			return c
		}
		ac, bc := policies[a.ID].State == "current", policies[b.ID].State == "current"
		if ac && !bc {
			return -1
		}
		if bc && !ac {
			return 1
		}
		ar, aok := a.MetaData["_keyword_rank"].(int)
		br, bok := b.MetaData["_keyword_rank"].(int)
		if aok && !bok {
			return -1
		}
		if bok && !aok {
			return 1
		}
		if aok && ar != br {
			return cmp.Compare(ar, br)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result[:min(topK, len(result))], nil
}

func NextDocumentPolicyChange(ctx context.Context, at time.Time) (time.Time, error) {
	documents, _, err := loadSearchSnapshot(ctx, "", 0)
	if err != nil {
		return time.Time{}, err
	}
	var next time.Time
	for _, item := range documents {
		value := item.policy.NextChange(at)
		if !value.IsZero() && (next.IsZero() || value.Before(next)) {
			next = value
		}
	}
	return next, nil
}
