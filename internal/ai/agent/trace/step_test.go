package trace

import (
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
)

func TestRecorderQualityCountsEvidenceAndToolFailures(t *testing.T) {
	recorder := NewRecorder()
	success := evidence.NewDataWithObservations(nil, "log result", []evidence.Observation{{Kind: evidence.ObservationLogError}})
	success.Evidence.EvidenceID = "ev-1"
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName: "query_logs",
		Envelope: success,
	})
	failure := evidence.NewError(nil, "metrics failed")
	failure.Failure.QueryID = "fq-1"
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName: "query_metrics",
		Envelope: failure,
	})

	evidenceCount, failedToolCount := recorder.QualityCounts()
	if evidenceCount != 1 || failedToolCount != 1 {
		t.Fatalf("unexpected quality counts: evidence=%d failed_tools=%d", evidenceCount, failedToolCount)
	}
}

func TestModelTokenUsageReadsChatModelCallbackOutput(t *testing.T) {
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	output := &model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens:       120,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 80},
		CompletionTokens:   30,
		TotalTokens:        150,
	}}

	promptTokens, cachedTokens, uncachedTokens, completionTokens, totalTokens := modelTokenUsage(info, output)
	if promptTokens != 120 || cachedTokens != 80 || uncachedTokens != 40 || completionTokens != 30 || totalTokens != 150 {
		t.Fatalf("unexpected tokens: prompt=%d cached=%d uncached=%d completion=%d total=%d", promptTokens, cachedTokens, uncachedTokens, completionTokens, totalTokens)
	}
}

func TestModelTokenUsageClampsInvalidCachedTokens(t *testing.T) {
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	output := &model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens:       120,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 500},
	}}
	_, cachedTokens, uncachedTokens, _, _ := modelTokenUsage(info, output)
	if cachedTokens != 120 || uncachedTokens != 0 {
		t.Fatalf("expected cached tokens clamped to prompt tokens, got cached=%d uncached=%d", cachedTokens, uncachedTokens)
	}
}
