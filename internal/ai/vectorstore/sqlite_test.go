package vectorstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

func TestKnowledgeVectorsRoundTripAndCosineOrdering(t *testing.T) {
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(t.TempDir(), "telltale.db"))
	ctx := context.Background()
	if err := storage.Initialize(ctx); err != nil {
		t.Fatalf("initialize sqlite storage: %v", err)
	}
	defer g.DB().Close(ctx)

	if err := UpsertKnowledgeDocuments(ctx,
		[]string{"near", "far"},
		[]string{"near document", "far document"},
		[]string{`{"_source":"test"}`, `{"_source":"test"}`},
		[][]float32{{1, 0}, {0, 1}},
	); err != nil {
		t.Fatalf("upsert knowledge vectors: %v", err)
	}

	documents, err := SearchKnowledge(ctx, []float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("search knowledge vectors: %v", err)
	}
	if len(documents) != 2 || documents[0].ID != "near" {
		t.Fatalf("expected cosine ordering [near far], got %#v", documents)
	}

	if err := DeleteKnowledgeBySource(ctx, "test"); err != nil {
		t.Fatalf("delete knowledge source: %v", err)
	}
	var count int
	count, err = g.DB().Model("knowledge_documents").Count()
	if err != nil {
		t.Fatalf("count knowledge documents: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected source documents to be deleted, got %d", count)
	}
}
