package vectorstore

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

// 固定向量仅用于验证融合算法和数据库一致性，不代表真实检索质量。
func checkHybridRetrieval(t *testing.T) {
	ctx := t.Context()
	if err := DeleteKnowledgeBySource(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	ids := []string{"semantic-near", "exact-code", "semantic-mid"}
	contents := []string{"service diagnostics overview", "错误 ERR_CARD_409：好友申请仅当PENDING且未过期时可接受。", "operational metrics overview"}
	meta := []string{`{"_source":"hybrid-fixture"}`, `{"_source":"hybrid-fixture","_context_prefix":"数据库锁等待","nested":{"name":"original"}}`, `{"_source":"hybrid-fixture"}`}
	if err := UpsertKnowledgeDocuments(ctx, ids, contents, meta, [][]float32{{1, 0}, {0, 1}, {0.8, 0.2}}); err != nil {
		t.Fatal(err)
	}
	dense, err := SearchKnowledge(ctx, []float32{1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range dense {
		if doc.ID == "exact-code" {
			t.Fatal("fixture did not exclude exact match from dense candidates")
		}
	}
	for _, query := range []string{"ERR_CARD_409", "好友申请", "锁等待"} {
		docs, err := SearchHybridKnowledge(ctx, query, []float32{1, 0}, 2, 60)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) != 2 || docs[0].ID != "exact-code" || docs[0].Content != contents[1] {
			t.Fatalf("hybrid failed for %s: %+v", query, docs)
		}
		if docs[0].MetaData["_keyword_rank"] != 1 || docs[0].MetaData["_retrieval"] != "sqlite_bm25_dense_rrf" {
			t.Fatal("rank provenance missing")
		}
	}
	docs, err := SearchHybridKnowledge(ctx, "ERR_CARD_409", []float32{1, 0}, 2, 60)
	if err != nil {
		t.Fatal(err)
	}
	docs[0].MetaData["nested"].(map[string]any)["name"] = "changed"
	docs, err = SearchHybridKnowledge(ctx, "ERR_CARD_409", []float32{1, 0}, 2, 60)
	if err != nil || docs[0].MetaData["nested"].(map[string]any)["name"] != "original" {
		t.Fatal("caller mutated cached snapshot")
	}
	before, _ := KnowledgeRevision(ctx)
	if err := UpsertKnowledgeDocuments(ctx, []string{"exact-code"}, []string{"NEW_CODE_ONLY 新规则"}, []string{meta[1]}, [][]float32{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	after, _ := KnowledgeRevision(ctx)
	if before == after {
		t.Fatal("low-level update failed to invalidate caches")
	}
	docs, err = SearchHybridKnowledge(ctx, "NEW_CODE_ONLY", []float32{1, 0}, 2, 60)
	if err != nil || docs[0].Content != "NEW_CODE_ONLY 新规则" {
		t.Fatalf("update not visible: %+v %v", docs, err)
	}
	docs, err = SearchHybridKnowledge(ctx, "ERR_CARD_409", []float32{1, 0}, 3, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		if _, ok := doc.MetaData["_keyword_rank"]; ok {
			t.Fatal("stale FTS terms survived update")
		}
	}
	if _, err := SearchHybridKnowledge(ctx, `" OR NOT *`, []float32{1, 0}, 2, 60); err != nil {
		t.Fatalf("query syntax escaped into MATCH: %v", err)
	}
	if _, err := SearchHybridKnowledge(ctx, "NEW_CODE_ONLY", []float32{1}, 2, 60); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
	if err := UpsertKnowledgeDocuments(ctx, []string{"atomic-new", "invalid"}, []string{"rollback", "invalid"}, []string{meta[0], "invalid-json"}, [][]float32{{1, 0}, {1, 0}}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if count, err := g.DB().Model("knowledge_documents").Where("id", "atomic-new").Count(); err != nil || count != 0 {
		t.Fatal("failed batch partially committed")
	}
	if revision, _ := KnowledgeRevision(ctx); revision != after {
		t.Fatal("failed batch changed revision")
	}
	if count, err := g.DB().GetCount(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, `"rollback"`); err != nil || count != 0 {
		t.Fatal("failed batch left orphaned FTS entries")
	}
	var rows []keywordRank
	if err := g.DB().GetScan(ctx, &rows, `SELECT d.id,bm25(knowledge_fts) AS score FROM knowledge_fts JOIN knowledge_documents d ON d.rowid=knowledge_fts.rowid WHERE knowledge_fts MATCH ?`, `"new_code_only"`); err != nil || len(rows) != 1 {
		t.Fatalf("FTS database mismatch: %v", err)
	}
	if err := DeleteKnowledgeBySource(ctx, "hybrid-fixture"); err != nil {
		t.Fatal(err)
	}
	if docs, err := SearchHybridKnowledge(ctx, "NEW_CODE_ONLY", []float32{1, 0}, 2, 60); err != nil || len(docs) != 0 {
		t.Fatalf("deleted docs remain: %v %v", docIDs(docs), err)
	}
}

func docIDs(docs []*schema.Document) []string {
	var ids []string
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return ids
}
