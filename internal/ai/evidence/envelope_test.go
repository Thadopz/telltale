package evidence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNewDataCreatesUsableEvidence(t *testing.T) {
	raw := Marshal(NewData([]string{"row"}, "found rows"))
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.Success || env.Status != StatusSuccess {
		t.Fatalf("expected success envelope, got %#v", env)
	}
	if !env.CanUseAsEvidence || !strings.HasPrefix(env.EvidenceID, "ev_") {
		t.Fatalf("expected usable evidence id, got %#v", env)
	}
	if env.DataStatus != DataPresent {
		t.Fatalf("expected present data, got %q", env.DataStatus)
	}
}

func TestNewDataWithObservationsKeepsStructuredFields(t *testing.T) {
	raw := Marshal(NewDataWithObservations([]string{"row"}, "found rows", []Observation{{
		Kind:    ObservationMetricAnomaly,
		Subject: SubjectRef{Type: "payment", Service: "payment"},
		Fields:  map[string]any{"metric_name": "payment_failed_rate"},
	}}))
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if len(env.Observations) != 1 || env.Observations[0].Kind != ObservationMetricAnomaly {
		t.Fatalf("expected structured observation, got %#v", env.Observations)
	}
	if env.Subject.Service != "payment" {
		t.Fatalf("expected subject from observation, got %#v", env.Subject)
	}
	if env.SourceKind != SourceRuntimeMetric || !env.RuntimeVerified {
		t.Fatalf("expected runtime metric provenance, got source=%q verified=%t", env.SourceKind, env.RuntimeVerified)
	}
}

func TestStaticEvidenceKeepsUntrustedUnverifiedProvenance(t *testing.T) {
	env := NewDataWithObservations("snippet", "static source", []Observation{{
		Kind:    ObservationStaticCodeDeclaration,
		Subject: SubjectRef{Type: "code", Service: "card-service"},
	}})
	env.SourceKind = SourceStaticCode
	env.Repository = "card_service"
	env.Revision = "rev-a"
	env.SnapshotDigest = "digest-a"
	env.VersionAlignment = VersionMismatch
	env.RuntimeVerified = false
	env.ContentTrust = ContentTrustUntrustedRepository
	parsed := Parse(Marshal(env)).Envelope
	if parsed.SourceKind != SourceStaticCode || parsed.RuntimeVerified || parsed.ContentTrust != ContentTrustUntrustedRepository {
		t.Fatalf("expected static provenance to survive round trip, got %#v", parsed)
	}
	if parsed.VersionAlignment != VersionMismatch || parsed.SnapshotDigest != "digest-a" {
		t.Fatalf("expected snapshot metadata to survive round trip, got %#v", parsed)
	}
}

func TestNewErrorCreatesFailedQuery(t *testing.T) {
	env := NewError(errors.New("prometheus request timed out"), "query timeout")
	if env.Success || env.Status != StatusTimeout {
		t.Fatalf("expected timeout envelope, got %#v", env)
	}
	if env.CanUseAsEvidence || env.EvidenceID != "" || !strings.HasPrefix(env.FailedQueryID, "fq_") {
		t.Fatalf("expected failed query id only, got %#v", env)
	}
	if env.DataStatus != DataUnknown {
		t.Fatalf("expected unknown data status, got %q", env.DataStatus)
	}
}

func TestParseLegacyFailureIsNotEvidence(t *testing.T) {
	parsed := Parse(`{"success":false,"error":"boom","message":"query failed"}`)
	if parsed.Envelope.Status != StatusError {
		t.Fatalf("expected error status, got %#v", parsed.Envelope)
	}
	if parsed.Envelope.CanUseAsEvidence || parsed.Envelope.EvidenceID != "" {
		t.Fatalf("legacy failure must not be evidence, got %#v", parsed.Envelope)
	}
	if !strings.HasPrefix(parsed.Envelope.FailedQueryID, "fq_") {
		t.Fatalf("expected generated failed query id, got %#v", parsed.Envelope)
	}
}
