package evidence

import (
	"testing"
	"time"
)

func TestIssueEnvelopeReplacesExternalEvidenceIdentity(t *testing.T) {
	env := NewDataWithObservations("runtime result", "observed", []Observation{{
		Kind: ObservationLogError,
	}})
	env.Evidence.EvidenceID = "ev_external_choice"
	env.Evidence.Attestation.IssuerTrust = ContentTrustUntrustedExternal
	env.Evidence.Provenance.RuntimeVerified = true
	env.Evidence.Provenance.VersionAlignment = VersionAligned

	issued := IssueEnvelope(env, IssueMetadata{
		Issuer:       "tool:remote_logs",
		RunID:        "run-local-1",
		TrustLevel:   ContentTrustUntrustedExternal,
		ExpiresAfter: time.Minute,
	})
	if issued.Evidence.EvidenceID == "ev_external_choice" || issued.Evidence.EvidenceID == "" {
		t.Fatalf("expected local evidence identity, got %q", issued.Evidence.EvidenceID)
	}
	if issued.Evidence.Attestation.Issuer != "tool:remote_logs" || issued.Evidence.Attestation.RunID != "run-local-1" {
		t.Fatalf("expected lifecycle issuer and run, got %#v", issued)
	}
	if issued.Evidence.Provenance.RuntimeVerified || issued.Evidence.Provenance.VersionAlignment != VersionUnknown {
		t.Fatalf("external result must not retain runtime trust, got %#v", issued.Evidence.Provenance)
	}
	if issued.Evidence.Attestation.ObservedAt.IsZero() || issued.Evidence.Attestation.ExpiresAt == nil || issued.Evidence.Attestation.ContentDigest == "" || issued.Evidence.Attestation.DigestVersion != 2 {
		t.Fatalf("expected complete lifecycle metadata, got %#v", issued)
	}
}

func TestMarkLocalFixtureEnvelopePreservesDeterministicID(t *testing.T) {
	env := NewDataWithObservations("fixture", "fixture result", []Observation{{Kind: ObservationMetricValue}})
	env.Evidence.EvidenceID = "ev_fixture"
	marked := MarkLocalFixtureEnvelope(env, IssueMetadata{Issuer: "eval:fixture", RunID: "run-eval"})
	if marked.Evidence.EvidenceID != "ev_fixture" {
		t.Fatalf("fixture adapter must preserve deterministic ID, got %q", marked.Evidence.EvidenceID)
	}
	if marked.Evidence.Attestation.Issuer != "eval:fixture" || marked.Evidence.Attestation.RunID != "run-eval" || marked.Evidence.Attestation.ObservedAt.IsZero() || marked.Evidence.Attestation.ContentDigest == "" {
		t.Fatalf("fixture metadata missing: %#v", marked)
	}
}

func TestDigestEvidenceRecordIgnoresRunSpecificAttestation(t *testing.T) {
	env := NewDataWithObservations("row", "metric", []Observation{{Kind: ObservationMetricValue, Fields: map[string]any{"value": 7}}})
	first := *env.Evidence
	second := first
	first.Attestation = Attestation{RunID: "run-a", TenantID: "tenant-a"}
	second.Attestation = Attestation{RunID: "run-b", TenantID: "tenant-b"}
	if DigestEvidenceRecord(first) != DigestEvidenceRecord(second) {
		t.Fatal("content digest must not change with run-specific audit metadata")
	}
}

func TestValidObservationRejectsUnknownKinds(t *testing.T) {
	if ValidObservation(Observation{Kind: "free_form_claim"}) {
		t.Fatal("unknown observation kind must not enter the matcher")
	}
	if ValidObservation(Observation{Kind: ObservationLogError}) == false {
		t.Fatal("registered observation kind should be accepted")
	}
}
