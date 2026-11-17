package main

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"strings"
	"testing"
)

func TestFixedCasesHaveSourceDisjointSplits(t *testing.T) {
	counts := map[string]int{}
	sources := map[string]string{}
	ids := map[string]bool{}
	for _, c := range cases {
		if ids[c.ID] || c.Question == "" || c.Expected == "" {
			t.Fatal("invalid case")
		}
		ids[c.ID] = true
		if split, ok := sources[c.Source]; ok && split != c.Split {
			t.Fatal("source leaked across development and holdout")
		}
		sources[c.Source] = c.Split
		counts[c.Split]++
	}
	if counts["dev"] != 4 || counts["holdout"] != 12 {
		t.Fatal("unexpected fixed split")
	}
}

func TestCurrentComparisonSharesIdentifierRepairAcrossAllArms(t *testing.T) {
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	adapter, err := gcfg.NewAdapterContent(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	id := strings.Repeat("ab", 32)
	env := evidence.NewDataWithObservations([]*schema.Document{{ID: id, Content: "仅对幂等请求重试。", MetaData: map[string]any{"_source": "manual.md", "_chunk_hash": id, "api_key": "secret"}}}, "short authorized documentation", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": id, "runbook_rule": "仅对幂等请求重试。", "metadata": map[string]any{"_source": "manual.md", "_chunk_hash": id}}}})
	raw := evidence.Marshal(env)
	frozen := evidence.Marshal(evidence.NewDataWithObservations(nil, "historical", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "<redacted:high-entropy>", "runbook_rule": "仅对幂等请求重试。"}}}))
	for _, arm := range []string{"baseline", "task", "generic"} {
		result := evidence.Parse(projectComparisonResult(t.Context(), raw, frozen, arm, "current")).Envelope
		if result.Evidence == nil || len(result.Evidence.Observations) != 1 {
			t.Fatalf("%s lost documentation", arm)
		}
		fields := result.Evidence.Observations[0].Fields
		if fields["document_id"] != id || fields["runbook_rule"] != "仅对幂等请求重试。" || fields["metadata"].(map[string]any)["_chunk_hash"] != id {
			t.Fatalf("%s did not share identifier handling", arm)
		}
		docs := result.Data.([]any)
		doc := docs[0].(map[string]any)
		metadata := doc["meta_data"].(map[string]any)
		if doc["id"] != id || metadata["_chunk_hash"] != id || metadata["api_key"] != "<redacted>" {
			t.Fatalf("%s did not safely preserve document data identifiers: %v", arm, doc)
		}
	}
	if got := projectComparisonResult(t.Context(), raw, frozen, "baseline", "historical"); got != frozen {
		t.Fatal("historical baseline was silently replaced")
	}
	if evidence.Marshal(env) != raw {
		t.Fatal("raw comparison input mutated")
	}
}
