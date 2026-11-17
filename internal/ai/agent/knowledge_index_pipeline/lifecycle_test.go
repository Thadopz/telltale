package knowledge_index_pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexerimpl "github.com/Thadopz/telltale/internal/ai/indexer"
	"github.com/Thadopz/telltale/internal/ai/knowledge"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestIndexLifecycle(t *testing.T) {
	ctx := t.Context()
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(t.TempDir(), "isolated.db"))
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	setConfig := func(config map[string]any) {
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := gcfg.NewAdapterContent(string(encoded))
		if err != nil {
			t.Fatal(err)
		}
		g.Cfg().SetAdapter(adapter)
	}
	setConfig(map[string]any{"knowledge_index": map[string]any{"contextual_enabled": false}})
	if err := storage.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.DB().Close(context.Background()) })
	transformer, err := newDocumentTransformer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parents := []*schema.Document{
		{Content: "# 服务甲\n## v2\n请求超时后重试。", MetaData: map[string]any{"_source": "a.md"}},
		{Content: "# 服务乙\n## v3\n请求超时后重试。", MetaData: map[string]any{"_source": "b.md"}},
	}
	first, err := transformer.Transform(ctx, parents)
	if err != nil {
		t.Fatal(err)
	}
	second, err := transformer.Transform(ctx, parents)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) || len(first) < 4 {
		t.Fatal("missing sections")
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].MetaData["_context_prefix"] != nil {
			t.Fatal("unstable baseline identity or generated prefix")
		}
	}
	if first[1].ID == first[3].ID || first[1].MetaData["_section_path"] == first[3].MetaData["_section_path"] {
		t.Fatal("subject identity lost")
	}
	// Seed storage integrity data; it is never used to measure retrieval quality.
	if err := vectorstore.ReplaceKnowledgeSource(ctx, "a.md", []string{"old"}, []string{"existing source"}, []string{`{"_source":"a.md"}`}, [][]float32{{1}}); err != nil {
		t.Fatal(err)
	}
	before, err := vectorstore.KnowledgeRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setConfig(map[string]any{"knowledge_index": map[string]any{"contextual_enabled": true}})
	if _, err := transformer.Transform(ctx, parents[:1]); err == nil {
		t.Fatal("unconfigured context model accepted")
	}
	docs, err := vectorstore.KnowledgeBySource(ctx, "a.md")
	if err != nil || len(docs) != 1 || docs[0].ID != "old" {
		t.Fatalf("context failure removed old source: %v", err)
	}
	after, err := vectorstore.KnowledgeRevision(ctx)
	if err != nil || before != after {
		t.Fatal("context failure changed index revision")
	}
	if err := vectorstore.ReplaceKnowledgeSource(ctx, "a.md", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TELLTALE_CONTEXTUAL_LIVE") != "1" {
		return
	}

	g.Cfg().SetAdapter(previous)
	appconfig.ConfigureAt(ctx, "../../../..")
	config, err := g.Cfg().Data(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"quick_chat_model", "embedding_model"} {
		value, err := g.Cfg().Get(ctx, role+".api_key")
		if err != nil || value.String() == "" || strings.Contains(value.String(), "YOUR_") {
			t.Fatalf("live %s is not configured", role)
		}
	}
	directory := t.TempDir()
	for name, content := range map[string]string{
		"atlas.md":  "# Atlas 服务运行手册\n版本 v2，2026 年 10 月适用。Atlas 使用队列处理图像任务。\n## 超时处理\n任务超过 30 秒后取消；只对幂等任务重试一次。\n",
		"boreal.md": "# Boreal 服务运行手册\n版本 v3，2026 年 9 月适用。Boreal 使用队列处理报表任务。\n## 超时处理\n任务超过 90 秒后取消；只对幂等任务重试两次。\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	queries := []struct{ query, source string }{
		{"Atlas v2 幂等任务超时后的取消阈值和重试次数", "atlas.md"},
		{"Boreal v3 报表任务的超时取消和重试规则", "boreal.md"},
		{"2026年10月图像服务的任务重试条件", "atlas.md"},
		{"2026年9月报表服务的任务取消阈值", "boreal.md"},
	}
	config["knowledge_index"] = map[string]any{"contextual_enabled": true}
	setConfig(config)
	generationStart := time.Now()
	generated, err := transformer.Transform(ctx, parents)
	if err != nil {
		t.Fatalf("live context generation: %v", err)
	}
	inputTokens, outputTokens := 0, 0
	for _, doc := range generated {
		if err := validateContext(doc.MetaData[vectorstore.ContextPrefixKey].(string)); err != nil {
			t.Fatal(err)
		}
		if doc.Content == doc.MetaData[vectorstore.ContextPrefixKey] {
			t.Fatal("prefix replaced source content")
		}
		if value, ok := doc.MetaData["_context_input_tokens"].(int); ok {
			inputTokens += value
		}
		if value, ok := doc.MetaData["_context_output_tokens"].(int); ok {
			outputTokens += value
		}
	}
	t.Logf("real context generation chunks=%d duration=%s provider_input_tokens=%d provider_output_tokens=%d", len(generated), time.Since(generationStart), inputTokens, outputTokens)
	for _, contextual := range []bool{false, true} {
		config["knowledge_index"] = map[string]any{"contextual_enabled": contextual}
		setConfig(config)
		started := time.Now()
		runner, err := BuildKnowledgeIndexing(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"atlas.md", "boreal.md"} {
			if _, err := runner.Invoke(ctx, document.Source{URI: filepath.Join(directory, name)}); err != nil {
				t.Fatalf("live contextual=%t index: %v", contextual, err)
			}
		}
		indexDuration := time.Since(started)
		hits := 0
		service := knowledge.NewService()
		for _, query := range queries {
			result, err := service.Search(ctx, knowledge.SearchRequest{SessionID: "live-contextual", Query: query.query, SkipRerank: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Documents) > 0 && strings.HasSuffix(result.Documents[0].MetaData["_source"].(string), query.source) {
				hits++
			}
		}
		t.Logf("contextual=%t synthetic sources=2 queries=%d source_hit@1=%d indexing=%s total=%s", contextual, len(queries), hits, indexDuration, time.Since(started))
		if contextual {
			before, err := vectorstore.KnowledgeRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			indexer, err := indexerimpl.NewSQLiteIndexer(ctx)
			if err != nil {
				t.Fatal(err)
			}
			docs, err := vectorstore.KnowledgeBySource(ctx, filepath.Join(directory, "atlas.md"))
			if err != nil || len(docs) == 0 {
				t.Fatalf("load indexed source: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := indexer.Store(canceled, docs); err == nil {
				t.Fatal("canceled embedding succeeded")
			}
			after, err := vectorstore.KnowledgeRevision(ctx)
			if err != nil || after != before {
				t.Fatal("embedding failure changed index")
			}
			started = time.Now()
			if _, err := runner.Invoke(ctx, document.Source{URI: filepath.Join(directory, "atlas.md")}); err != nil {
				t.Fatal(err)
			}
			t.Logf("unchanged contextual reindex=%s (context cache reused; embeddings regenerated)", time.Since(started))
		}
	}
}
