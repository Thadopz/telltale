package knowledge_index_pipeline

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

func TestStructuralSourceMappingAndIdentity(t *testing.T) {
	for _, content := range []string{
		"中文。English sentence.🙂\n\n没有标题。",
		"Setext title\n============\n中文正文。\n",
		"# 第一节\n正文。\n## 子节\n引用之前。\n> 引用\n\n- 项目一\n- 项目二\n\n# 第二节\n正文。",
		"# Code\n```go\n# not a heading\n" + strings.Repeat("println(\"你好\")\n", 70) + "```\n",
		"## Table\n| A | B |\n|---|---|\n" + strings.Repeat("| 中文 | value |\n", 80),
		strings.Repeat("中文🙂", 300),
		strings.Repeat("This is a short sentence. 中文句子。", 100),
		"\n\n---\n\n````\n````\n",
	} {
		parent := &schema.Document{Content: content, MetaData: map[string]any{"_source": "test.md", "version": "v2", "access": "private"}}
		transformer := &structuralTransformer{limit: 512}
		first, err := transformer.Transform(t.Context(), []*schema.Document{parent})
		if err != nil {
			t.Fatal(err)
		}
		second, err := transformer.Transform(t.Context(), []*schema.Document{parent})
		if err != nil || len(first) != len(second) {
			t.Fatal("unstable chunk count", err)
		}
		for i, doc := range first {
			if doc.ID != second[i].ID || !utf8.ValidString(doc.Content) || len(doc.Content) > 512 || doc.Content == "" {
				t.Fatalf("invalid chunk: %+v", doc)
			}
			if doc.MetaData["access"] != "private" || doc.MetaData["version"] != "v2" {
				t.Fatal("metadata lost")
			}
			var reconstructed strings.Builder
			reconstructed.WriteString(doc.MetaData["_synthetic_prefix"].(string))
			for _, span := range doc.MetaData["_source_ranges"].([]sourceRange) {
				if span.Start < 0 || span.End > len(content) || span.Start >= span.End || !utf8.ValidString(content[span.Start:span.End]) {
					t.Fatal("invalid Unicode mapping", span)
				}
				reconstructed.WriteString(content[span.Start:span.End])
			}
			reconstructed.WriteString(doc.MetaData["_synthetic_suffix"].(string))
			if reconstructed.String() != doc.Content {
				t.Fatal("mapping differs from displayed text")
			}
			if doc.MetaData["_overlap_bytes"].(int) > 48 {
				t.Fatal("overlap exceeds limit")
			}
			if strings.Contains(doc.Content, "not a heading") && doc.MetaData["_section_path"] != "Code" {
				t.Fatal("code content interpreted as heading")
			}
		}
	}
}
