package evidence

import (
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"strings"
	"testing"
)

func TestLargeCurrentOutputRemainsParseableAndPartial(t *testing.T) {
	previousTokens := 0
	for _, rows := range []int{10000, 100000} {
		logs := make([]any, rows)
		for i := range logs {
			logs[i] = map[string]any{"message": strings.Repeat("中文堆栈", 10), "reference": "log_123456789012345678901234"}
		}
		env := NewDataWithObservations(map[string]any{"logs": logs, "query_ref": "log_query", "statistics_coverage": "complete"}, "raw", []Observation{{Kind: ObservationLogError, Subject: SubjectRef{Service: "payment"}, Summary: "observed error"}})
		view := BoundOutput(env)
		raw := Marshal(view)
		if !json.Valid([]byte(raw)) || contextbudget.EstimateTextTokens(raw) > ToolOutputTokens-400 {
			t.Fatal("current result exceeds budget or is malformed")
		}
		if view.DataStatus != DataPartial || view.Evidence == nil || !view.Evidence.ResultMetadata.Truncated || view.Evidence.EvidenceID != env.Evidence.EvidenceID {
			t.Fatal("missing partial metadata or evidence reference")
		}
		if !strings.Contains(raw, "log_query") || strings.Contains(raw, `"statistics_coverage":"complete"`) {
			t.Fatal("reference lost or misleading complete coverage")
		}
		if env.Evidence.ResultMetadata.Truncated {
			t.Fatal("audit source mutated")
		}
		t.Logf("source_bytes=%d projected_bytes=%d projected_estimated_tokens=%d", len(Marshal(env)), len(raw), contextbudget.EstimateTextTokens(raw))
		tokens := contextbudget.EstimateTextTokens(raw)
		if previousTokens != 0 && tokens != previousTokens {
			t.Fatal("tenfold duplicate growth changed the model projection size")
		}
		previousTokens = tokens
	}
}
