package chat_pipeline

import (
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"testing"
	"time"
)

func TestMatchClaimSetUsesExplicitEvidenceAndUnifiedRules(t *testing.T) {
	claimSet := evidence.ClaimSet{
		SchemaVersion: evidence.ClaimSchemaVersion,
		Claims: []evidence.CandidateClaim{{
			ClaimID:       "claim-payment-log",
			ClaimType:     evidence.ClaimPaymentLogError,
			AssertionKind: evidence.AssertionFact,
			EvidenceIDs:   []string{"ev_log"},
			Subject:       evidence.SubjectRef{Service: "payment"},
		}},
	}
	reports := matchClaimSet(claimSet, []evidence.EvidenceItem{{
		EvidenceID:      "ev_log",
		SourceKind:      evidence.SourceRuntimeLog,
		RuntimeVerified: true,
		ObservedAt:      time.Now().UTC(),
		Observations:    []evidence.Observation{{Kind: evidence.ObservationLogError, Subject: evidence.SubjectRef{Service: "payment"}}},
	}}, nil)
	if len(reports) != 1 || reports[0].Status != evidence.CoverageCovered {
		t.Fatalf("expected explicit payment claim to be covered, got %#v", reports)
	}
}

func TestEvidenceFromToolEvidenceKeepsStructuredEnvelope(t *testing.T) {
	envelope := evidence.NewDataWithObservations(nil, "runtime log", []evidence.Observation{{Kind: evidence.ObservationLogError}})
	envelope.Evidence.EvidenceID = "ev_log"
	envelope.Evidence.Provenance.SourceKind = evidence.SourceRuntimeLog
	envelope.Evidence.Provenance.RuntimeVerified = true
	envelope.Evidence.Attestation.ObservedAt = time.Now().UTC()
	items, failed := evidenceFromToolEvidence([]trace.ToolEvidence{{
		ToolName: "runtime_logs",
		Envelope: envelope,
	}})
	if len(failed) != 0 || len(items) != 1 {
		t.Fatalf("expected one structured evidence item, items=%#v failed=%#v", items, failed)
	}
	if items[0].EvidenceID != "ev_log" || items[0].SourceKind != evidence.SourceRuntimeLog {
		t.Fatalf("structured evidence was not preserved: %#v", items[0])
	}
}
