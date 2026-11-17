package knowledge_index_pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/cloudwego/eino/components/document"
)

func TestFileLoaderReadsExplicitPolicyWithoutChangingOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runbook.md")
	text := "# 规则\n禁止自动重启。"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	loader, err := newLoader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loader.Load(t.Context(), document.Source{URI: path})
	if err != nil {
		t.Fatal(err)
	}
	record, err := documentpolicy.FromMetadata(docs[0].MetaData)
	if err != nil || record.State != "unverified" || docs[0].Content != text {
		t.Fatal("unannotated file was changed or inferred current")
	}
	metadata := `{"state":"current","version":"v2","verified_at":"2026-11-04T00:00:00Z","verified_by":"owner"}`
	if err := os.WriteFile(path+".meta.json", []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	docs, err = loader.Load(t.Context(), document.Source{URI: path})
	if err != nil {
		t.Fatal(err)
	}
	record, err = documentpolicy.FromMetadata(docs[0].MetaData)
	if err != nil || record.State != "current" || record.Version != "v2" || docs[0].Content != text {
		t.Fatal("sidecar declaration not preserved")
	}
	if err := os.WriteFile(path+".meta.json", []byte(`{"state":"current"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(t.Context(), document.Source{URI: path}); err == nil {
		t.Fatal("invalid sidecar accepted")
	}
}
