package retriever

import (
	"testing"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

func TestDocumentsFromHybridResultsPreservesMetadataAndRRFScore(t *testing.T) {
	results := []milvusclient.ResultSet{{
		ResultCount: 2,
		IDs:         column.NewColumnVarChar("id", []string{"doc-1", "doc-2"}),
		Fields: milvusclient.DataSet{
			column.NewColumnVarChar("content", []string{"first", "second"}),
			column.NewColumnJSONBytes("metadata", [][]byte{[]byte(`{"_source":"runbook.md"}`), []byte(`{}`)}),
		},
		Scores: []float32{0.9, 0.7},
	}}

	documents, err := documentsFromHybridResults(results)
	if err != nil {
		t.Fatalf("convert hybrid results: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("expected two documents, got %d", len(documents))
	}
	if documents[0].ID != "doc-1" || documents[0].Content != "first" {
		t.Fatalf("unexpected first document: %+v", documents[0])
	}
	if documents[0].MetaData["_source"] != "runbook.md" {
		t.Fatalf("source metadata was not preserved: %+v", documents[0].MetaData)
	}
	if documents[0].MetaData["_retrieval"] != "dense_bm25_rrf" {
		t.Fatalf("retrieval strategy metadata missing: %+v", documents[0].MetaData)
	}
	if documents[0].Score() <= documents[1].Score() {
		t.Fatalf("expected RRF order to be preserved: first=%f second=%f", documents[0].Score(), documents[1].Score())
	}
}

func TestDocumentsFromHybridResultsRejectsMissingContent(t *testing.T) {
	_, err := documentsFromHybridResults([]milvusclient.ResultSet{{
		ResultCount: 1,
		IDs:         column.NewColumnVarChar("id", []string{"doc-1"}),
	}})
	if err == nil {
		t.Fatal("expected missing content error")
	}
}
