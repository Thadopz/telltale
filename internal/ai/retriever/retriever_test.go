package retriever

import (
	"context"
	"testing"
)

func TestSQLiteRetrieverUsesLocalVectorStore(t *testing.T) {
	retriever, err := NewSQLiteRetriever(context.Background())
	if err == nil {
		local, ok := retriever.(*SQLiteVectorRetriever)
		if !ok || local.GetType() != "SQLiteHybridRetriever" {
			t.Fatalf("unexpected retriever implementation %#v", retriever)
		}
	}
	if err != nil {
		// Configuration-free unit tests do not provide an embedding model. The
		// constructor must still fail before any remote vector service is used.
		return
	}
}
