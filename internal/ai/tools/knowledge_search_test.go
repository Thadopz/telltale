package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/schema"
)

func TestKnowledgeSearchBudgetSharesDelegationsAndIsolatesRuns(t *testing.T) {
	ctx := WithKnowledgeSearchBudget(t.Context())
	state := ctx.Value(knowledgeSearchKey{}).(*knowledgeSearchState)
	if WithKnowledgeSearchBudget(ctx).Value(knowledgeSearchKey{}) != state {
		t.Fatal("nested delegation reset the search budget")
	}
	if err := state.begin("  CALLBACK\nrunbook "); err != nil {
		t.Fatal(err)
	}
	if err := state.begin("callback runbook"); err == nil {
		t.Fatal("equivalent query was accepted")
	}
	for i := 1; i < knowledgeSearchLimit; i++ {
		if err := state.begin(fmt.Sprintf("distinct gap %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	tool, err := NewQueryInternalDocsTool()
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.InvokableRun(ctx, `{"query":"another gap"}`)
	if err != nil {
		t.Fatal(err)
	}
	env := evidence.Parse(output).Envelope
	if env.Status != evidence.StatusError || !strings.Contains(env.Failure.Reason, "budget exhausted") {
		t.Fatalf("tool did not enforce the exhausted budget: %s", output)
	}
	other := WithKnowledgeSearchBudget(t.Context()).Value(knowledgeSearchKey{}).(*knowledgeSearchState)
	if err := other.begin("callback runbook"); err != nil {
		t.Fatalf("another run inherited search history: %v", err)
	}
}

func TestKnowledgeSearchStopsWithoutNewDocuments(t *testing.T) {
	state := WithKnowledgeSearchBudget(t.Context()).Value(knowledgeSearchKey{}).(*knowledgeSearchState)
	document := &schema.Document{ID: "runbook", Content: "Inspect the blocking transaction."}
	for i := range 3 {
		if err := state.begin(fmt.Sprintf("gap %d", i)); err != nil {
			t.Fatal(err)
		}
		state.observe([]*schema.Document{nil, document})
	}
	if err := state.begin("unresolved version"); err == nil {
		t.Fatal("retrieval continued after two searches without new documents")
	}
}

func TestKnowledgeSearchRecognizesChangedContentAndResetsNoProgress(t *testing.T) {
	state := WithKnowledgeSearchBudget(t.Context()).Value(knowledgeSearchKey{}).(*knowledgeSearchState)
	document := &schema.Document{ID: "runbook", Content: "Version one."}
	state.observe([]*schema.Document{document})
	state.observe([]*schema.Document{document})
	document.Content = "Version two supersedes version one."
	if progress := state.observe([]*schema.Document{document}); !strings.Contains(progress, "1 new documents, 0 consecutive") {
		t.Fatalf("changed document did not reset progress: %s", progress)
	}
	if err := state.begin("version applicability"); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeDocumentsProduceTraceableNonRuntimeEvidence(t *testing.T) {
	documents := []*schema.Document{nil, {ID: "empty"}, {
		ID: "locking-v2", Content: "Check lock waits before terminating a transaction.",
		MetaData: map[string]any{"source": "database-runbook.md", "version": "2"},
	}}
	raw := marshalToolEvidence(documents, "retrieved documentation", knowledgeDocumentObservations(documents))
	env := evidence.Parse(raw).Envelope
	if err := env.Validate(); err != nil {
		t.Fatal(err)
	}
	record, ok := env.UsableEvidence()
	if !ok || record.Provenance.RuntimeVerified || len(record.Observations) != 1 {
		t.Fatalf("documentation must be usable without proving live state: %s", raw)
	}
	observation := record.Observations[0]
	if observation.Kind != evidence.ObservationRunbookRule || observation.Fields["document_id"] != "locking-v2" || observation.Fields["runbook_rule"] != documents[2].Content {
		t.Fatalf("document provenance or content was lost: %s", raw)
	}
	if !strings.Contains(raw, "database-runbook.md") || !strings.Contains(raw, `"version":"2"`) {
		t.Fatalf("source metadata was lost: %s", raw)
	}
}
