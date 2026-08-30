package diagnosis

import (
	"math"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/agent/trace"
)

func TestSummarizeTraceAggregatesCachedPromptTokens(t *testing.T) {
	recorder := trace.NewRecorder()
	recorder.Record(trace.AgentStep{PromptTokens: 100, CachedPromptTokens: 80, UncachedPromptTokens: 20, CacheHitRatio: 0.8})
	recorder.Record(trace.AgentStep{PromptTokens: 50, CachedPromptTokens: 10, UncachedPromptTokens: 40, CacheHitRatio: 0.2})

	summary := summarizeTrace(recorder)
	if summary.PromptTokens != 150 || summary.CachedPromptTokens != 90 || summary.UncachedPromptTokens != 60 {
		t.Fatalf("unexpected cache totals: %#v", summary)
	}
	if math.Abs(summary.CacheHitRatio-0.6) > 0.0001 {
		t.Fatalf("expected token-weighted cache hit ratio 0.6, got %f", summary.CacheHitRatio)
	}
}
