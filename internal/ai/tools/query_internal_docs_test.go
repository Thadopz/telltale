package tools

import (
	"github.com/cloudwego/eino/schema"
	"testing"
)

func TestKnowledgeEvidenceQuotesOnlySourceChunk(t *testing.T) {
	doc := &schema.Document{ID: "a", Content: "原始运行手册规则", MetaData: map[string]any{"_source": "a.md", "_context_prefix": "模型生成的服务说明"}}
	observations := knowledgeDocumentObservations([]*schema.Document{doc})
	if len(observations) != 1 || observations[0].Fields["runbook_rule"] != doc.Content {
		t.Fatal("generated context entered source quotation")
	}
	meta := observations[0].Fields["metadata"].(map[string]any)
	if meta["_context_prefix"] != "模型生成的服务说明" || meta["_source"] != "a.md" {
		t.Fatal("context and provenance missing")
	}
}
