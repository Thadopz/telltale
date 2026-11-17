package answer

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
	"time"
)

func TestCollectEvidenceReportsConflictingDuplicateIDs(t *testing.T) {
	first := evidence.NewDataWithObservations("first", "first", []evidence.Observation{{
		Kind:   evidence.ObservationLogError,
		Fields: map[string]any{"error_code": "ERR_ONE"},
	}})
	first.Evidence.EvidenceID = "ev_same"
	first.Evidence.Attestation.ObservedAt = time.Now().UTC()
	second := evidence.NewDataWithObservations("second", "second", []evidence.Observation{{
		Kind:   evidence.ObservationLogError,
		Fields: map[string]any{"error_code": "ERR_TWO"},
	}})
	second.Evidence.EvidenceID = "ev_same"
	second.Evidence.Attestation.ObservedAt = time.Now().UTC()

	collection := collectEvidence(nil, []trace.ToolEvidence{
		{ToolName: "logs_a", Envelope: first},
		{ToolName: "logs_b", Envelope: second},
	})
	if len(collection.Items) != 1 || len(collection.Conflicts) != 1 {
		t.Fatalf("expected first item plus one conflict, items=%#v conflicts=%#v", collection.Items, collection.Conflicts)
	}
	if collection.Items[0].Observations[0].Fields["error_code"] != "ERR_ONE" {
		t.Fatalf("conflicting later value must not overwrite first, got %#v", collection.Items[0])
	}
}

func TestConvergeAnswerTreatsPlainSuccessAsNonEvidence(t *testing.T) {
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "diagnose the backend",
		Answer:   "the backend is failing",
		ToolEvidence: []trace.ToolEvidence{{
			ToolName: "backend_health",
			Envelope: evidence.NewData(map[string]any{"healthy": false}, "backend check returned data"),
		}},
	})
	if err != nil {
		t.Fatalf("protocol result should produce a deterministic gap answer: %v", err)
	}
	if !strings.Contains(answer, "structured evidence") {
		t.Fatalf("expected protocol issue in answer, got %q", answer)
	}
}
