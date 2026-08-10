package evidence

import (
	"testing"
	"time"
)

func TestIssueEnvelopeReplacesExternalEvidenceIdentity(t *testing.T) {
	env := NewDataWithObservations("runtime result", "observed", []Observation{{
		Kind: ObservationLogError,
	}})
	env.EvidenceID = "ev_external_choice"
	env.TrustLevel = ContentTrustUntrustedExternal
	env.RuntimeVerified = true
	env.VersionAlignment = VersionAligned

	issued := IssueEnvelope(env, IssueMetadata{
		Issuer:       "tool:remote_logs",
		RunID:        "run-local-1",
		TrustLevel:   ContentTrustUntrustedExternal,
		ExpiresAfter: time.Minute,
	})
	if issued.EvidenceID == "ev_external_choice" || issued.EvidenceID == "" {
		t.Fatalf("expected local evidence identity, got %q", issued.EvidenceID)
	}
	if issued.Issuer != "tool:remote_logs" || issued.RunID != "run-local-1" {
		t.Fatalf("expected lifecycle issuer and run, got %#v", issued)
	}
	if issued.RuntimeVerified || issued.VersionAlignment != VersionUnknown {
		t.Fatalf("external result must not retain runtime trust, got verified=%t alignment=%q", issued.RuntimeVerified, issued.VersionAlignment)
	}
	if issued.ObservedAt.IsZero() || issued.ExpiresAt == nil || issued.ContentDigest == "" {
		t.Fatalf("expected complete lifecycle metadata, got %#v", issued)
	}
}

func TestMarkLocalFixtureEnvelopePreservesDeterministicID(t *testing.T) {
	env := NewDataWithObservations("fixture", "fixture result", []Observation{{Kind: ObservationMetricValue}})
	env.EvidenceID = "ev_fixture"
	marked := MarkLocalFixtureEnvelope(env, IssueMetadata{Issuer: "eval:fixture", RunID: "run-eval"})
	if marked.EvidenceID != "ev_fixture" {
		t.Fatalf("fixture adapter must preserve deterministic ID, got %q", marked.EvidenceID)
	}
	if marked.Issuer != "eval:fixture" || marked.RunID != "run-eval" || marked.ObservedAt.IsZero() || marked.ContentDigest == "" {
		t.Fatalf("fixture metadata missing: %#v", marked)
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
