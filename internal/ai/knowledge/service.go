package knowledge

import (
	knowledgeRetriever "github.com/Thadopz/telltale/internal/ai/retriever"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/observability"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

const defaultCacheTTL = 2 * time.Minute
const defaultCacheMaxEntries = 256

type SearchRequest struct {
	SessionID  string
	Query      string
	Retriever  retriever.Retriever
	SkipRerank bool
}

type SearchResult struct {
	Documents       []*schema.Document
	NormalizedQuery string
	CandidateCount  int
	RerankError     error
	FromCache       bool
}

type cacheEntry struct {
	result    SearchResult
	expiresAt time.Time
}

// Service owns the complete knowledge search path and its short-lived cache.
// A custom Retriever can still be supplied by tests and evaluation runs.
type Service struct {
	mu               sync.Mutex
	defaultRetriever retriever.Retriever
	cache            map[string]cacheEntry
}

func NewService() *Service {
	return &Service{cache: make(map[string]cacheEntry)}
}

var defaultService = NewService()

func DefaultService() *Service {
	return defaultService
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (result SearchResult, err error) {
	result.NormalizedQuery = NormalizeQuery(request.Query)
	if result.NormalizedQuery == "" {
		return result, fmt.Errorf("knowledge query is empty")
	}

	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "knowledge.retrieve",
		attribute.String("retrieval.strategy", "dense_bm25_rrf_rerank"),
	)
	defer func() {
		observability.RecordRetrieval(ctx, time.Since(startedAt), err, result.CandidateCount, len(result.Documents))
		observability.EndSpan(span, err,
			attribute.Int("retrieval.candidates", result.CandidateCount),
			attribute.Int("retrieval.final_documents", len(result.Documents)),
			attribute.Bool("retrieval.cache_hit", result.FromCache),
		)
	}()

	cacheKey := buildCacheKey(cacheSessionScope(ctx, request.SessionID), result.NormalizedQuery)
	if cached, ok := s.cachedResult(cacheKey, time.Now()); ok {
		cached.FromCache = true
		result = cached
		return result, nil
	}

	searcher := request.Retriever
	if searcher == nil {
		searcher, err = s.getDefaultRetriever(ctx)
		if err != nil {
			return result, err
		}
	}

	documents, err := searcher.Retrieve(ctx, request.Query)
	if err != nil {
		return result, err
	}
	result.CandidateCount = len(documents)
	result.Documents = documents

	if !request.SkipRerank && len(documents) > 0 {
		reranked, rerankErr := Rerank(ctx, request.Query, documents)
		if rerankErr != nil {
			// Retrieval remains usable when the external reranker is unavailable.
			result.RerankError = rerankErr
			g.Log().Warningf(ctx, "rerank failed, fallback to original docs, err=%v", rerankErr)
		} else {
			result.Documents = reranked
		}
	}

	s.storeResult(ctx, cacheKey, result, time.Now())
	return result, nil
}

func (s *Service) getDefaultRetriever(ctx context.Context) (retriever.Retriever, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.defaultRetriever != nil {
		return s.defaultRetriever, nil
	}
	created, err := knowledgeRetriever.NewSQLiteRetriever(ctx)
	if err != nil {
		return nil, fmt.Errorf("create knowledge retriever: %w", err)
	}
	s.defaultRetriever = created
	return s.defaultRetriever, nil
}

func (s *Service) cachedResult(key string, now time.Time) (SearchResult, bool) {
	if key == "" {
		return SearchResult{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok {
		return SearchResult{}, false
	}
	if !now.Before(entry.expiresAt) {
		delete(s.cache, key)
		return SearchResult{}, false
	}
	return cloneResult(entry.result), true
}

func (s *Service) storeResult(ctx context.Context, key string, result SearchResult, now time.Time) {
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	removeExpiredEntries(s.cache, now)
	if len(s.cache) >= cacheMaxEntries(ctx) {
		removeOldestEntry(s.cache)
	}
	s.cache[key] = cacheEntry{
		result:    cloneResult(result),
		expiresAt: now.Add(cacheTTL(ctx)),
	}
}

func NormalizeQuery(query string) string {
	var normalized strings.Builder
	lastWasSpace := false
	for _, character := range strings.ToLower(query) {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			if normalized.Len() > 0 && !lastWasSpace {
				normalized.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		normalized.WriteRune(character)
		lastWasSpace = false
	}
	return strings.TrimSpace(normalized.String())
}

func buildCacheKey(sessionID string, normalizedQuery string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || normalizedQuery == "" {
		return ""
	}
	return sessionID + "\x00" + normalizedQuery
}

func cacheSessionScope(ctx context.Context, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	principal := authz.PrincipalFromContext(ctx)
	return strings.Join([]string{
		strings.TrimSpace(principal.TenantID),
		strings.TrimSpace(principal.UserID),
		sessionID,
	}, "\x00")
}

func cloneResult(result SearchResult) SearchResult {
	cloned := result
	cloned.Documents = cloneDocuments(result.Documents)
	return cloned
}

func cloneDocuments(documents []*schema.Document) []*schema.Document {
	cloned := make([]*schema.Document, 0, len(documents))
	for _, document := range documents {
		if document == nil {
			continue
		}
		copyDocument := *document
		if document.MetaData != nil {
			copyDocument.MetaData = make(map[string]any, len(document.MetaData))
			for key, value := range document.MetaData {
				copyDocument.MetaData[key] = value
			}
		}
		cloned = append(cloned, &copyDocument)
	}
	return cloned
}

func removeExpiredEntries(cache map[string]cacheEntry, now time.Time) {
	for key, entry := range cache {
		if !now.Before(entry.expiresAt) {
			delete(cache, key)
		}
	}
}

func removeOldestEntry(cache map[string]cacheEntry) {
	oldestKey := ""
	var oldestExpiry time.Time
	for key, entry := range cache {
		if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(cache, oldestKey)
	}
}

func cacheTTL(ctx context.Context) time.Duration {
	value, err := g.Cfg().Get(ctx, "knowledge_retrieval.cache_ttl_seconds")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultCacheTTL
	}
	return time.Duration(value.Int()) * time.Second
}

func cacheMaxEntries(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "knowledge_retrieval.cache_max_entries")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultCacheMaxEntries
	}
	return value.Int()
}
