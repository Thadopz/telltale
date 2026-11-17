package knowledge_index_pipeline

import (
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/cloudwego/eino/schema"
	"maps"
	"strings"
	"testing"
)

func TestContextBudgetUsesExplicitScopeAndRejectsOversizedSection(t *testing.T) {
	profile := contextbudget.ModelContextProfile{ContextWindowTokens: 8192, MaxOutputTokens: 1024}
	messages, scope, err := contextMessages(profile, "service.md", "服务甲/v2", strings.Repeat("中", 20000), "服务甲 v2 2026", "需要重试")
	if err != nil || scope != "section" || !strings.Contains(messages[1].Content, `"scope":"section"`) || !strings.Contains(messages[1].Content, "服务甲") {
		t.Fatalf("scope=%s err=%v", scope, err)
	}
	_, _, err = contextMessages(profile, "service.md", "服务甲", strings.Repeat("中", 20000), strings.Repeat("中", 20000), "分块")
	if err == nil {
		t.Fatal("oversized section accepted")
	}
	_, scope, err = contextMessages(profile, "service.md", "服务甲", "完整原文", "章节", "分块")
	if err != nil || scope != "document" {
		t.Fatalf("scope=%s err=%v", scope, err)
	}
}

func TestContextCacheIdentityAndOutputValidation(t *testing.T) {
	current := &schema.Document{Content: "需要重试", MetaData: map[string]any{"_source": "a", "_source_hash": "parent", "_chunk_hash": "chunk", "_section_path": "甲/v2", "_context_prompt_version": contextPromptVersion, "_context_model": "quick:model"}}
	old := &schema.Document{Content: current.Content, MetaData: maps.Clone(current.MetaData)}
	old.MetaData["_context_prefix"], old.MetaData["_context_scope"] = "服务甲 v2", "document"
	if !reusableContext(old, current) {
		t.Fatal("unchanged context not reused")
	}
	for key := range current.MetaData {
		changed := &schema.Document{Content: current.Content, MetaData: maps.Clone(current.MetaData)}
		changed.MetaData[key] = "changed"
		if reusableContext(old, changed) {
			t.Fatalf("stale context reused after %s changed", key)
		}
	}
	if validateContext("") == nil || validateContext(strings.Repeat("中", 161)) == nil {
		t.Fatal("invalid prefix accepted")
	}
	text := strings.Repeat("中文🙂abc", 400)
	chunks := splitChunk(text, 351)
	if strings.Join(chunks, "") != text {
		t.Fatal("chunking lost source content")
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 351 {
			t.Fatal("chunk budget exceeded")
		}
	}
}
