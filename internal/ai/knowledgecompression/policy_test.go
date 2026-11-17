package knowledgecompression

import (
	"reflect"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestParagraphSelectionRetainsHistoricalPolicy(t *testing.T) {
	policy := documentpolicy.Record{State: "deprecated", Version: "v1", ValidUntil: "2026-11-04T00:00:00Z", SupersededBy: "v2.md"}
	metadata := map[string]any{documentpolicy.MetadataKey: policy}
	env := evidence.NewDataWithObservations(nil, "history", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "old", "runbook_rule": "仅在幂等请求时重试。\n\n禁止重试已经扣款的订单。", "metadata": metadata}}})
	paragraphs, err := Paragraphs(env)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Apply(env, paragraphs, []int{0}, "goal")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected.Evidence.Observations[0].Fields["metadata"], metadata) {
		t.Fatal("compression lost version or replacement boundary")
	}
}
