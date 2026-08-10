package knowledge

import (
	"github.com/Thadopz/telltale/internal/authz"
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

type countingRetriever struct {
	calls     int
	documents []*schema.Document
	err       error
}

func (r *countingRetriever) Retrieve(context.Context, string, ...retriever.Option) ([]*schema.Document, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.documents, nil
}

func TestSearchCachesNormalizedQueryWithinSession(t *testing.T) {
	service := NewService()
	searcher := &countingRetriever{documents: []*schema.Document{{Content: "runbook"}}}

	first, err := service.Search(context.Background(), SearchRequest{
		SessionID:  "conversation-1",
		Query:      "  Payment\nRUNBOOK  ",
		Retriever:  searcher,
		SkipRerank: true,
	})
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	second, err := service.Search(context.Background(), SearchRequest{
		SessionID:  "conversation-1",
		Query:      "payment runbook",
		Retriever:  searcher,
		SkipRerank: true,
	})
	if err != nil {
		t.Fatalf("second search: %v", err)
	}

	if searcher.calls != 1 {
		t.Fatalf("expected one retriever call, got %d", searcher.calls)
	}
	if first.FromCache {
		t.Fatal("expected first search not to use cache")
	}
	if !second.FromCache {
		t.Fatal("expected normalized second search to use cache")
	}
	if second.NormalizedQuery != "payment runbook" {
		t.Fatalf("unexpected normalized query %q", second.NormalizedQuery)
	}
}

func TestSearchDoesNotReuseCacheWhenIntentQueryChanges(t *testing.T) {
	service := NewService()
	searcher := &countingRetriever{documents: []*schema.Document{{Content: "runbook"}}}

	for _, query := range []string{"payment callback runbook", "mysql slow sql runbook"} {
		_, err := service.Search(context.Background(), SearchRequest{
			SessionID:  "conversation-2",
			Query:      query,
			Retriever:  searcher,
			SkipRerank: true,
		})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
	}

	if searcher.calls != 2 {
		t.Fatalf("expected changed query to run retrieval twice, got %d", searcher.calls)
	}
}

func TestSearchDoesNotShareCacheAcrossSessions(t *testing.T) {
	service := NewService()
	searcher := &countingRetriever{documents: []*schema.Document{{Content: "runbook"}}}

	for _, sessionID := range []string{"conversation-a", "conversation-b"} {
		_, err := service.Search(context.Background(), SearchRequest{
			SessionID:  sessionID,
			Query:      "same runbook",
			Retriever:  searcher,
			SkipRerank: true,
		})
		if err != nil {
			t.Fatalf("search session %q: %v", sessionID, err)
		}
	}

	if searcher.calls != 2 {
		t.Fatalf("expected one retrieval per session, got %d", searcher.calls)
	}
}

func TestSearchDoesNotShareCacheAcrossUsers(t *testing.T) {
	service := NewService()
	searcher := &countingRetriever{documents: []*schema.Document{{Content: "runbook"}}}

	for _, userID := range []string{"user-a", "user-b"} {
		ctx := authz.WithPrincipal(context.Background(), authz.Principal{TenantID: "tenant", UserID: userID})
		_, err := service.Search(ctx, SearchRequest{
			SessionID:  "same-conversation-id",
			Query:      "same runbook",
			Retriever:  searcher,
			SkipRerank: true,
		})
		if err != nil {
			t.Fatalf("search user %q: %v", userID, err)
		}
	}

	if searcher.calls != 2 {
		t.Fatalf("expected one retrieval per user, got %d", searcher.calls)
	}
}

func TestSearchDoesNotCacheFailures(t *testing.T) {
	service := NewService()
	searcher := &countingRetriever{err: errors.New("sqlite retriever unavailable")}

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Search(context.Background(), SearchRequest{
			SessionID:  "conversation-failure",
			Query:      "payment runbook",
			Retriever:  searcher,
			SkipRerank: true,
		})
		if err == nil {
			t.Fatal("expected retrieval failure")
		}
	}

	if searcher.calls != 2 {
		t.Fatalf("expected failures not to be cached, got %d calls", searcher.calls)
	}
}
