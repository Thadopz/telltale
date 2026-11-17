package knowledge_index_pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino-ext/components/document/transformer/splitter/markdown"
	"github.com/cloudwego/eino/schema"
)

func TestExportChunkingExperiment(t *testing.T) {
	directory := os.Getenv("TELLTALE_CHUNKING_EXPORT")
	if directory == "" {
		t.Skip("offline experiment export requires explicit directory")
	}
	baseline, err := markdown.NewHeaderSplitter(t.Context(), &markdown.HeaderConfig{
		Headers: map[string]string{
			"#":      "title",
			"##":     "heading2",
			"###":    "heading3",
			"####":   "heading4",
			"#####":  "heading5",
			"######": "heading6",
		},
		TrimHeaders: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]*schema.Document{}
	for _, name := range []string{"chatlogs", "state_of_the_union", "wikitexts"} {
		data, err := os.ReadFile(filepath.Join(directory, "upstream/chunking_evaluation/evaluation_framework/general_evaluation_data/corpora", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		parent := &schema.Document{Content: string(data), MetaData: map[string]any{"_source": name}}
		sections, err := baseline.Transform(t.Context(), []*schema.Document{parent})
		if err != nil {
			t.Fatal(err)
		}
		cursor := 0
		for _, section := range sections {
			for _, content := range splitChunk(section.Content, 351) {
				// 旧方法默认输入 2048 字节，实际 rune 上限为 (2048-644)/4。
				var ranges []sourceRange
				for line := range strings.SplitSeq(content, "\n") {
					if line == "" {
						continue
					}
					position := strings.Index(parent.Content[cursor:], line)
					if position < 0 {
						t.Fatalf("baseline source mapping failed: %s", name)
					}
					start := cursor + position
					cursor = start + len(line)
					ranges = append(ranges, sourceRange{Start: start, End: cursor})
				}
				groups["A"] = append(groups["A"], &schema.Document{
					Content: content,
					MetaData: map[string]any{
						"_source":        name,
						"_source_ranges": ranges,
					},
				})
			}
		}
		optimized, err := (&structuralTransformer{limit: 512}).Transform(t.Context(), []*schema.Document{parent})
		if err != nil {
			t.Fatal(err)
		}
		groups["B"] = append(groups["B"], optimized...)
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	if existing, err := os.ReadFile(filepath.Join(directory, "chunks.json")); err == nil {
		var frozen map[string][]*schema.Document
		if err := json.Unmarshal(existing, &frozen); err != nil {
			t.Fatal(err)
		}
		for group, docs := range groups {
			if len(docs) != len(frozen[group]) {
				t.Fatal("frozen chunk count changed", group)
			}
			for i, doc := range docs {
				if doc.Content != frozen[group][i].Content || doc.ID != frozen[group][i].ID {
					t.Fatal("frozen chunk text or identity changed", group, i)
				}
			}
		}
		return
	}
	if err := os.WriteFile(filepath.Join(directory, "chunks.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
