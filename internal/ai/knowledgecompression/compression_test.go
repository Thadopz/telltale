package knowledgecompression

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalParagraphSelectionAndBoundaryFailures(t *testing.T) {
	env := evidence.NewDataWithObservations("duplicate payload", "retrieved", []evidence.Observation{
		{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "v1", "metadata": map[string]any{"_source": "manual", "version": "1", "_context_prefix": "generated"}, "runbook_rule": "版本1\n\n仅在幂等请求时重试。\n\n禁止重试已经扣款的订单。\n\n其他背景"}},
		{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "v2", "metadata": map[string]any{"_source": "manual", "version": "2"}, "runbook_rule": "版本2禁止自动重试。"}},
	})
	env.Evidence.Attestation = evidence.Attestation{ContentDigest: "old"}
	original := evidence.Marshal(env)
	paragraphs, err := Paragraphs(env)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Apply(env, paragraphs, []int{1, 4}, GoalHash(Goal{Question: "比较版本1和2的重试条件"}))
	if err != nil {
		t.Fatal(err)
	}
	if selected.Data != nil || selected.DataStatus != evidence.DataPartial || selected.Evidence.Attestation.ContentDigest != "" {
		t.Fatal("selection retained duplicate payload or old signature")
	}
	text := selected.Evidence.Observations[0].Fields["runbook_rule"].(string)
	if !strings.Contains(text, "仅在") || !strings.Contains(text, "禁止重试") || !strings.HasPrefix(text, "版本1") || len(selected.Evidence.Observations) != 2 {
		t.Fatal("lost conditions or conflict")
	}
	if env.Evidence.Observations[0].Fields["selection"] != nil || evidence.Marshal(env) != original {
		t.Fatal("mutated original")
	}
	for _, ids := range [][]int{nil, {-1}, {99}, {1, 1}} {
		if _, err := Apply(env, paragraphs, ids, "goal"); err == nil {
			t.Fatalf("accepted invalid selection %v", ids)
		}
	}
	if !reflect.DeepEqual(env.Evidence.Observations[0].Fields["metadata"], selected.Evidence.Observations[0].Fields["metadata"]) {
		t.Fatal("provenance changed")
	}
	goal := Goal{Question: "比较版本1和2的重试条件"}
	again, report, err := Select(WithGoal(t.Context(), goal.Question, ""), selected, goal, "task", nil)
	if err != nil || evidence.Marshal(again) != evidence.Marshal(selected) || report.Calls != 0 {
		t.Fatal("selection was not idempotent")
	}
	if _, _, err := Select(WithGoal(t.Context(), "其他问题", ""), selected, Goal{Question: "其他问题"}, "task", nil); err == nil {
		t.Fatal("old selection reused for a new goal")
	}
	signed := evidence.IssueEnvelope(selected, evidence.IssueMetadata{Issuer: "tool:query_internal_docs", RunID: "run", TenantID: "tenant"})
	if signed.Evidence.Attestation.ContentDigest == "old" || signed.Evidence.Attestation.ContentDigest != evidence.DigestEvidenceRecord(*signed.Evidence) || evidence.Marshal(env) != original {
		t.Fatal("old attestation reused or original audit mutated")
	}
}

func TestNoSelectionForShortOrFailedResultsAndGoalIsolation(t *testing.T) {
	failed := evidence.NewError(nil, "query failed")
	for _, policy := range []string{"task", "generic"} {
		got, report, err := Select(t.Context(), failed, Goal{}, policy, nil)
		if err != nil || evidence.Marshal(got) != evidence.Marshal(failed) || report.Calls != 0 {
			t.Fatal("failure or short result changed")
		}
		short := evidence.NewDataWithObservations(nil, "short", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "short", "runbook_rule": "仅执行已批准的操作。"}}})
		got, report, err = Select(t.Context(), short, Goal{}, policy, nil)
		if err != nil || evidence.Marshal(got) != evidence.Marshal(short) || report.Calls != 0 {
			t.Fatal("short knowledge result invoked a selector")
		}
	}
	ctx := WithGoal(t.Context(), "问题甲", "缺口甲")
	if GoalFromContext(WithGoal(ctx, "问题乙", "缺口乙")) == GoalFromContext(ctx) {
		t.Fatal("goal leaked across request scopes")
	}
}

func TestInvalidSelectorJSONAndFiniteRequestBudget(t *testing.T) {
	for _, text := range []string{"not JSON", `{"ids":[0],"text":"rewritten"}`, `{"ids":[0]} trailing`, `{"ids":[0]} {"ids":[1]}`} {
		if _, err := decodeSelection(text); err == nil {
			t.Fatalf("accepted selector output %q", text)
		}
	}
	ctx := WithGoal(t.Context(), "问题", "")
	env := evidence.NewDataWithObservations(nil, "large", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": "doc", "runbook_rule": strings.Repeat("有条件的规则。", 1000)}}})
	for range 5 {
		if _, _, err := Select(ctx, env, Goal{Question: "问题"}, "task", nil); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("expected no configured model: %v", err)
		}
	}
	if _, _, err := Select(ctx, env, Goal{Question: "问题"}, "task", nil); err == nil || !strings.Contains(err.Error(), "call budget exhausted") {
		t.Fatalf("request limit not enforced: %v", err)
	}
	other := WithGoal(t.Context(), "问题", "")
	if _, _, err := Select(other, env, Goal{Question: "问题"}, "task", nil); err == nil || strings.Contains(err.Error(), "call budget exhausted") {
		t.Fatal("request scopes shared a call budget")
	}
}
