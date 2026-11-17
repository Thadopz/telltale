package indexer

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPrepareKnowledgeColumnsRejectsWrongDenseDimension(t *testing.T) {
	documents := []*schema.Document{{ID: "doc-1", Content: "runbook"}}
	_, _, _, _, err := prepareKnowledgeColumns(context.Background(), documents, [][]float64{{0.1, 0.2}})
	if err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("expected dimension mismatch, got %v", err)
	}
}

func TestCanceledRealEmbeddingPreservesSource(t *testing.T) {
	ctx := t.Context()
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(t.TempDir(), "index.db"))
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	adapter, err := gcfg.NewAdapterContent(`{"embedding_model":{"model":"qwen3-vl-embedding","api_key":"unused-canceled-request","dimension":1152}}`)
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	if err := storage.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.DB().Close(context.Background()) })
	// This vector only seeds a persisted record to check failure atomicity.
	if err := vectorstore.ReplaceKnowledgeSource(ctx, "source", []string{"old"}, []string{"old original"}, []string{`{"_source":"source"}`}, [][]float32{{1}}); err != nil {
		t.Fatal(err)
	}
	before, err := vectorstore.KnowledgeRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := NewSQLiteIndexer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := indexer.Store(canceled, []*schema.Document{{ID: "new", Content: "new original", MetaData: map[string]any{"_source": "source"}}}); err == nil || !strings.Contains(err.Error(), "embed knowledge documents") {
		t.Fatalf("expected real canceled HTTP embedding error: %v", err)
	}
	docs, err := vectorstore.KnowledgeBySource(ctx, "source")
	if err != nil || len(docs) != 1 || docs[0].ID != "old" {
		t.Fatalf("lost old source: %+v %v", docs, err)
	}
	after, err := vectorstore.KnowledgeRevision(ctx)
	if err != nil || before != after {
		t.Fatal("failed embedding changed revision")
	}
}
