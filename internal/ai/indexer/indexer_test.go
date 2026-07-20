package indexer

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPrepareKnowledgeColumnsRejectsWrongDenseDimension(t *testing.T) {
	documents := []*schema.Document{{ID: "doc-1", Content: "runbook"}}
	_, _, _, _, err := prepareKnowledgeColumns(context.Background(), documents, [][]float64{{0.1, 0.2}})
	if err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("expected dimension mismatch, got %v", err)
	}
}
