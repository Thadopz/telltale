package trace

import (
	"testing"

	"SREagent/internal/ai/evidence"
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
