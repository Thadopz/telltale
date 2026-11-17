package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

func TestKnowledgeAnnotateCLIUsesMetadataWithoutModelCalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(dir, "policy.db"))
	if err := storage.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.DB().Close(context.Background()) })
	source := "cli-policy-fixture"
	if err := vectorstore.UpsertKnowledgeDocuments(t.Context(), []string{"cli-policy-doc"}, []string{"原文保持不变"}, []string{`{"_source":"cli-policy-fixture"}`}, [][]float32{{1, 0}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vectorstore.DeleteKnowledgeBySource(context.Background(), source) })
	file := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(file, []byte(`{"state":"deprecated","version":"v1","superseded_by":"v2.md"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runKnowledgeAnnotate(t.Context(), []string{"--source", source, "--metadata", file}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1 indexed chunks") {
		t.Fatal("update count missing")
	}
	docs, err := vectorstore.KnowledgeBySource(t.Context(), source)
	if err != nil || len(docs) != 1 {
		t.Fatalf("source missing: %v", err)
	}
	record, err := documentpolicy.FromMetadata(docs[0].MetaData)
	if err != nil || record.State != "deprecated" || docs[0].Content != "原文保持不变" {
		t.Fatal("CLI policy update failed")
	}
	if err := runKnowledgeAnnotate(t.Context(), nil, &output); err == nil {
		t.Fatal("missing source accepted")
	}
}
