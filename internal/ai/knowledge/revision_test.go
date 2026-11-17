package knowledge

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceReplacementInvalidatesSharedSearchCache(t *testing.T) {
	ctx := t.Context()
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(t.TempDir(), "cache.db"))
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	adapter, err := gcfg.NewAdapterContent(`{"embedding_model":{"model":"multimodal-embedding-v1","api_key":""}}`)
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	if err := storage.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.DB().Close(context.Background()) })
	service := NewService()
	request := SearchRequest{SessionID: "session", Query: "服务规则", SkipRerank: true}
	revision, err := vectorstore.KnowledgeRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := buildCacheKey(cacheSessionScope(ctx, request.SessionID), NormalizeQuery(request.Query)) + "\x00skip-rerank\x00" + revision
	service.storeResult(ctx, key, SearchResult{Documents: []*schema.Document{{Content: "old"}}}, time.Now())
	result, err := service.Search(ctx, request)
	if err != nil || !result.FromCache || result.Documents[0].Content != "old" {
		t.Fatalf("cache unavailable: %v", err)
	}
	enabled := request
	enabled.SkipRerank = false
	if _, err := service.Search(ctx, enabled); err == nil {
		t.Fatal("reranked search reused an unranked cache entry")
	}
	if result, err := service.Search(WithRerankDisabled(ctx), enabled); err != nil || !result.FromCache {
		t.Fatalf("request-wide rerank setting missed the unranked cache: %v", err)
	}
	if err := vectorstore.ReplaceKnowledgeSource(ctx, "empty", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err = service.Search(ctx, request)
	if err == nil || result.FromCache || !strings.Contains(err.Error(), "api_key is empty") {
		t.Fatalf("expected fresh retrieval configuration failure, got cache=%t err=%v", result.FromCache, err)
	}
}
