package vectorstore

import (
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"testing"
)

func TestRetrievalTextSeparatesOriginalAndSupportsLegacy(t *testing.T) {
	doc := &schema.Document{Content: "原始规则", MetaData: map[string]any{ContextPrefixKey: "服务甲 v2"}}
	if RetrievalText(doc) != "服务甲 v2\n\n原始规则" || doc.Content != "原始规则" {
		t.Fatal("source changed")
	}
	doc.MetaData = nil
	if RetrievalText(doc) != doc.Content {
		t.Fatal("legacy content changed")
	}
}

func checkReplaceSourceAtomicIdempotentAndIsolated(t *testing.T) {
	ctx := t.Context()
	// Vectors exercise storage integrity only; this test makes no retrieval-quality claim.
	replace := func(source string, ids []string) error {
		contents, metadata, vectors := make([]string, len(ids)), make([]string, len(ids)), make([][]float32, len(ids))
		for index := range ids {
			contents[index], metadata[index], vectors[index] = "原始分块", `{"_source":"`+source+`","_context_prefix":"生成说明"}`, []float32{1}
		}
		return ReplaceKnowledgeSource(ctx, source, ids, contents, metadata, vectors)
	}
	if err := replace("a", []string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	if err := replace("b", []string{"b1"}); err != nil {
		t.Fatal(err)
	}
	before, err := KnowledgeRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := replace("a", []string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	if count, err := g.DB().Model("knowledge_documents").Count(); err != nil || count != 3 {
		t.Fatalf("duplicate rows: %d %v", count, err)
	}
	if err := replace("a", []string{"new"}); err != nil {
		t.Fatal(err)
	}
	after, err := KnowledgeRevision(ctx)
	if err != nil || before == after {
		t.Fatalf("revision unchanged: %v", err)
	}
	docs, err := KnowledgeBySource(ctx, "a")
	if err != nil || len(docs) != 1 || docs[0].ID != "new" || docs[0].Content != "原始分块" {
		t.Fatalf("replacement: %+v %v", docs, err)
	}
	if _, err := g.DB().Exec(ctx, `CREATE TRIGGER reject_failure BEFORE INSERT ON knowledge_documents WHEN NEW.id = 'failure' BEGIN SELECT RAISE(ABORT, 'intentional constraint failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := replace("a", []string{"partial", "failure"}); err == nil {
		t.Fatal("expected SQLite constraint failure")
	}
	docs, err = KnowledgeBySource(ctx, "a")
	if err != nil || len(docs) != 1 || docs[0].ID != "new" {
		t.Fatalf("transaction lost original: %+v %v", docs, err)
	}
	revision, err := KnowledgeRevision(ctx)
	if err != nil || revision != after {
		t.Fatal("failed transaction changed revision")
	}
	if err := replace("a", []string{"b1"}); err == nil {
		t.Fatal("cross-source ID collision accepted")
	}
	if err := replace("a", nil); err != nil {
		t.Fatal(err)
	}
	docs, err = KnowledgeBySource(ctx, "b")
	if err != nil || len(docs) != 1 || docs[0].ID != "b1" {
		t.Fatal("unrelated source changed")
	}
	if count, err := g.DB().Model("knowledge_documents").Count(); err != nil || count != 1 {
		t.Fatalf("deleted chunks remain: %d %v", count, err)
	}
	if revision, err = KnowledgeRevision(ctx); err != nil || revision == after {
		t.Fatal("empty replacement did not invalidate revision")
	}
}
