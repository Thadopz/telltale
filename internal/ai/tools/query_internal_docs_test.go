package tools

import (
	"SREagent/internal/ai/knowledge"
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

type knowledgeToolRetriever struct {
	calls int
}

func (r *knowledgeToolRetriever) Retrieve(context.Context, string, ...retriever.Option) ([]*schema.Document, error) {
	r.calls++
	return []*schema.Document{{Content: "shared knowledge result"}}, nil
}

func TestQueryInternalDocsUsesSuccessfulPreRetrievalCache(t *testing.T) {
	ctx := context.WithValue(context.Background(), "client_id", "tool-cache-"+t.Name())
	searcher := &knowledgeToolRetriever{}
	query := "payment callback runbook " + t.Name()

	_, err := knowledge.DefaultService().Search(ctx, knowledge.SearchRequest{
		SessionID:  "tool-cache-" + t.Name(),
		Query:      query,
		Retriever:  searcher,
		SkipRerank: true,
	})
	if err != nil {
		t.Fatalf("seed pre-retrieval result: %v", err)
	}

	knowledgeTool, err := NewQueryInternalDocsTool()
	if err != nil {
		t.Fatalf("create knowledge tool: %v", err)
	}
	output, err := knowledgeTool.InvokableRun(ctx, `{"query":"PAYMENT   CALLBACK RUNBOOK `+t.Name()+`"}`)
	if err != nil {
		t.Fatalf("invoke knowledge tool: %v", err)
	}

	if searcher.calls != 1 {
		t.Fatalf("expected tool not to access Milvus after successful pre-retrieval, got %d calls", searcher.calls)
	}
	if !strings.Contains(output, "session query cache") {
		t.Fatalf("expected cache result message, got %s", output)
	}
}
