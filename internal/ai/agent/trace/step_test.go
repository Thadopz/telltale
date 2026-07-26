package trace

import (
	"testing"

	"SREagent/internal/ai/evidence"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
)

func TestRecorderQualityCountsEvidenceAndToolFailures(t *testing.T) {
	recorder := NewRecorder()
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName: "query_logs",
		Envelope: evidence.Envelope{EvidenceID: "ev-1", Status: "success"},
	})
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName: "query_metrics",
		Envelope: evidence.Envelope{FailedQueryID: "fq-1", Status: "error"},
	})

	evidenceCount, failedToolCount := recorder.QualityCounts()
	if evidenceCount != 1 || failedToolCount != 1 {
		t.Fatalf("unexpected quality counts: evidence=%d failed_tools=%d", evidenceCount, failedToolCount)
	}
}

func TestModelTokenUsageReadsChatModelCallbackOutput(t *testing.T) {
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	output := &model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens:     120,
		CompletionTokens: 30,
		TotalTokens:      150,
	}}

	promptTokens, completionTokens, totalTokens := modelTokenUsage(info, output)
	if promptTokens != 120 || completionTokens != 30 || totalTokens != 150 {
		t.Fatalf("unexpected tokens: prompt=%d completion=%d total=%d", promptTokens, completionTokens, totalTokens)
	}
}
