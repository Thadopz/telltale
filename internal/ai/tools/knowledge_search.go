package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"sync"

	"github.com/Thadopz/telltale/internal/ai/knowledge"
	"github.com/cloudwego/eino/schema"
)

const knowledgeSearchLimit = 5

type knowledgeSearchKey struct{}

type knowledgeSearchState struct {
	mu        sync.Mutex
	queries   map[string]bool
	documents map[[32]byte]bool
	noNew     int
}

// WithKnowledgeSearchBudget shares retrieval progress across delegations in one run.
func WithKnowledgeSearchBudget(ctx context.Context) context.Context {
	if _, ok := ctx.Value(knowledgeSearchKey{}).(*knowledgeSearchState); ok {
		return ctx
	}
	return context.WithValue(ctx, knowledgeSearchKey{}, &knowledgeSearchState{
		queries:   make(map[string]bool),
		documents: make(map[[32]byte]bool),
	})
}

func (s *knowledgeSearchState) begin(query string) error {
	return s.beginScoped(query, "")
}

func (s *knowledgeSearchState) beginScoped(query, scope string) error {
	query = knowledge.NormalizeQuery(query)
	if query == "" {
		return fmt.Errorf("knowledge query is empty")
	}
	query += scope
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) >= knowledgeSearchLimit || s.noNew >= 2 {
		return fmt.Errorf("knowledge retrieval stopped: search budget exhausted or two searches returned no new documents; answer from collected evidence and report remaining gaps")
	}
	if s.queries[query] {
		return fmt.Errorf("knowledge query already searched; use its previous evidence or reformulate for a distinct unresolved gap")
	}
	s.queries[query] = true
	return nil
}

func (s *knowledgeSearchState) observe(documents []*schema.Document) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	newDocuments := 0
	for _, document := range documents {
		if document == nil {
			continue
		}
		policy, _ := json.Marshal(document.MetaData[documentpolicy.MetadataKey])
		key := sha256.Sum256([]byte(document.ID + "\x00" + document.Content + "\x00" + string(policy)))
		if !s.documents[key] {
			s.documents[key] = true
			newDocuments++
		}
	}
	if newDocuments == 0 {
		s.noNew++
	} else {
		s.noNew = 0
	}
	return fmt.Sprintf("; retrieval progress: %d/%d searches, %d new documents, %d consecutive searches without new documents", len(s.queries), knowledgeSearchLimit, newDocuments, s.noNew)
}
