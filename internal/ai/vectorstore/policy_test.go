package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/gogf/gf/v2/frame/g"
)

func checkDocumentPolicyRetrieval(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	var ids, contents, metadata []string
	var vectors [][]float32
	add := func(id string, policy documentpolicy.Record) {
		encoded, _ := json.Marshal(map[string]any{"_source": "policy-fixture", documentpolicy.MetadataKey: policy})
		ids = append(ids, id)
		contents = append(contents, "scope_policy_marker")
		metadata = append(metadata, string(encoded))
		vectors = append(vectors, []float32{1, 0})
	}
	for i := range 70 {
		add(fmt.Sprintf("a-old-%02d", i), documentpolicy.Record{State: "deprecated", Version: "v1", SupersededBy: "new.md"})
	}
	current := documentpolicy.Record{State: "current", Version: "v2", VerifiedAt: "2026-11-03T00:00:00Z", VerifiedBy: "owner"}
	add("z-current", current)
	add("m-unverified", documentpolicy.Record{State: "unverified"})
	add("a-planned", documentpolicy.Record{State: "planned", Version: "v3"})
	expired := current
	expired.ValidUntil = now.Format(time.RFC3339Nano)
	add("a-expired", expired)
	future := current
	future.ValidFrom = now.Add(time.Second).Format(time.RFC3339Nano)
	add("a-future", future)
	if err := UpsertKnowledgeDocuments(t.Context(), ids, contents, metadata, vectors); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = DeleteKnowledgeBySource(context.Background(), "policy-fixture") })
	filter, _ := (documentpolicy.Options{}).Resolve(now)
	ctx := documentpolicy.WithFilter(t.Context(), filter)
	docs, err := SearchHybridKnowledge(ctx, "scope_policy_marker", []float32{1, 0}, 2, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("invalid sources crowded out eligible results: %v", docIDs(docs))
	}
	for _, doc := range docs {
		if doc.ID != "z-current" && doc.ID != "m-unverified" {
			t.Fatalf("ineligible document leaked: %s", doc.ID)
		}
		if _, ok := doc.MetaData["_keyword_rank"]; !ok {
			t.Fatal("keyword page filtering lost valid sources")
		}
	}
	filter.Version = "v2"
	docs, err = SearchHybridKnowledge(documentpolicy.WithFilter(ctx, filter), "scope_policy_marker", []float32{1, 0}, 2, 60)
	if err != nil || len(docs) != 1 || docs[0].ID != "z-current" {
		t.Fatalf("exact version filtering failed: %v %v", docIDs(docs), err)
	}
	historical, _ := (documentpolicy.Options{Mode: "history", Version: "v1"}).Resolve(now)
	docs, err = SearchHybridKnowledge(documentpolicy.WithFilter(ctx, historical), "scope_policy_marker", []float32{1, 0}, 2, 60)
	if err != nil || len(docs) != 2 {
		t.Fatalf("historical version inaccessible: %v", err)
	}
	for _, doc := range docs {
		r, err := documentpolicy.FromMetadata(doc.MetaData)
		if err != nil || r.State != "deprecated" || r.SupersededBy != "new.md" {
			t.Fatal("historical provenance missing")
		}
	}
	boundary, err := NextDocumentPolicyChange(ctx, now)
	if err != nil || !boundary.Equal(now.Add(time.Second)) {
		t.Fatalf("cache activation boundary missing: %v %v", boundary, err)
	}
	filter.At = boundary
	docs, err = SearchHybridKnowledge(documentpolicy.WithFilter(ctx, filter), "scope_policy_marker", []float32{1, 0}, 4, 60)
	if err != nil || len(docs) != 2 {
		t.Fatalf("future source did not activate at boundary: %v %v", docIDs(docs), err)
	}
	before, _ := KnowledgeRevision(ctx)
	replacement := documentpolicy.Record{State: "deprecated", Version: "v1", SupersededBy: "replacement.md"}
	if count, err := UpdateKnowledgePolicy(ctx, "policy-fixture", replacement); err != nil || count != len(ids) {
		t.Fatalf("metadata-only update failed: %d %v", count, err)
	}
	if after, _ := KnowledgeRevision(ctx); after == before {
		t.Fatal("policy update did not invalidate caches")
	}
	if doc, err := g.DB().GetOne(ctx, `SELECT content,vector FROM knowledge_documents WHERE id='z-current'`); err != nil || doc["content"].String() != "scope_policy_marker" || doc["vector"].String() != "[1,0]" {
		t.Fatal("policy update changed source or vector")
	}
	filter.Version = ""
	if docs, err := SearchHybridKnowledge(documentpolicy.WithFilter(ctx, filter), "scope_policy_marker", []float32{1, 0}, 2, 60); err != nil || len(docs) != 0 {
		t.Fatal("policy update did not immediately affect retrieval")
	}
}
