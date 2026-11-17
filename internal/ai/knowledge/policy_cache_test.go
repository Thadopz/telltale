package knowledge

import (
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/cloudwego/eino/schema"
)

func TestPolicyCacheExpiresAtValidityBoundaryAndSeparatesScopes(t *testing.T) {
	service := NewService()
	now := time.Now()
	boundary := now.Add(time.Second)
	current, _ := (documentpolicy.Options{}).Resolve(now)
	history, _ := (documentpolicy.Options{Mode: "history", Version: "v1"}).Resolve(now)
	key := buildCacheKey("policy-session", "rules")
	service.storeResult(t.Context(), key+current.CacheKey(), SearchResult{Documents: []*schema.Document{{ID: "current"}}, ValidUntil: boundary}, now)
	service.storeResult(t.Context(), key+history.CacheKey(), SearchResult{Documents: []*schema.Document{{ID: "old"}}}, now)
	if result, ok := service.cachedResult(key, now); !ok || result.Documents[0].ID != "current" {
		t.Fatal("current cache unavailable")
	}
	if _, ok := service.cachedResult(key, boundary); ok {
		t.Fatal("current cache survived validity boundary")
	}
	if result, ok := service.cachedResult(key+history.CacheKey(), boundary); !ok || result.Documents[0].ID != "old" {
		t.Fatal("historical scope cache was mixed with current")
	}
}
