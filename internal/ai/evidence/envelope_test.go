package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestNewDataWithoutObservationsIsSuccessfulButNotEvidence(t *testing.T) {
	env := Parse(Marshal(NewData([]string{"row"}, "found rows"))).Envelope
	if env.Status != StatusSuccess || env.ProtocolVersion != ProtocolVersion {
		t.Fatalf("expected protocol v2 success, got %#v", env)
	}
	if _, ok := env.UsableEvidence(); ok || env.Evidence != nil {
		t.Fatalf("plain data must not become evidence, got %#v", env.Evidence)
	}
	if env.DataStatus != DataPresent {
		t.Fatalf("expected present data, got %q", env.DataStatus)
	}
}

func TestNewDataWithObservationsCreatesEvidencePayload(t *testing.T) {
	env := Parse(Marshal(NewDataWithObservations([]string{"row"}, "found rows", []Observation{{
		Kind:    ObservationMetricAnomaly,
		Subject: SubjectRef{Type: "payment", Service: "payment"},
		Fields:  map[string]any{"metric_name": "payment_failed_rate"},
	}}))).Envelope
	record, ok := env.UsableEvidence()
	if !ok || !strings.HasPrefix(record.EvidenceID, "ev_") {
		t.Fatalf("expected usable evidence payload, got %#v", env)
	}
	if len(record.Observations) != 1 || record.Observations[0].Subject.Service != "payment" {
		t.Fatalf("expected observation-owned subject, got %#v", record.Observations)
	}
	if record.Provenance.SourceKind != SourceRuntimeMetric || !record.Provenance.RuntimeVerified {
		t.Fatalf("expected runtime metric provenance, got %#v", record.Provenance)
	}
}

func TestStaticEvidenceKeepsNestedProvenance(t *testing.T) {
	env := NewDataWithObservations("snippet", "static source", []Observation{{Kind: ObservationStaticCodeDeclaration}})
	env.Evidence.Provenance = Provenance{
		SourceKind: SourceStaticCode, Repository: "card_service", Revision: "rev-a",
		SnapshotDigest: "digest-a", VersionAlignment: VersionMismatch,
		PayloadTrust: ContentTrustUntrustedRepository,
	}
	record, ok := Parse(Marshal(env)).Envelope.UsableEvidence()
	if !ok || record.Provenance.SourceKind != SourceStaticCode || record.Provenance.PayloadTrust != ContentTrustUntrustedRepository {
		t.Fatalf("expected static provenance to survive round trip, got %#v", record)
	}
	if record.Provenance.VersionAlignment != VersionMismatch || record.Provenance.SnapshotDigest != "digest-a" {
		t.Fatalf("expected snapshot metadata to survive round trip, got %#v", record.Provenance)
	}
}

func TestNewErrorCreatesFailurePayload(t *testing.T) {
	env := NewError(errors.New("prometheus request timed out"), "query timeout")
	if env.Status != StatusTimeout || env.Failure == nil {
		t.Fatalf("expected timeout failure, got %#v", env)
	}
	if !strings.HasPrefix(env.Failure.QueryID, "fq_") || env.Evidence != nil {
		t.Fatalf("expected failed query only, got %#v", env)
	}
}

func TestParseRejectsLegacyEnvelope(t *testing.T) {
	parsed := Parse(`{"success":false,"error":"boom","message":"query failed"}`)
	if parsed.Envelope.Status != StatusProtocolError || parsed.Envelope.Failure == nil {
		t.Fatalf("legacy envelope must become a protocol gap, got %#v", parsed.Envelope)
	}
	again := Parse(`{"success":false,"error":"boom","message":"query failed"}`)
	if parsed.Envelope.Failure.QueryID != again.Envelope.Failure.QueryID {
		t.Fatalf("persisted protocol gap id must be stable: %q != %q", parsed.Envelope.Failure.QueryID, again.Envelope.Failure.QueryID)
	}
}

func TestParseRejectsUnknownV2Fields(t *testing.T) {
	parsed := Parse(`{"protocol_version":"2","status":"success","data_status":"empty","success":true}`)
	if parsed.Envelope.Status != StatusProtocolError {
		t.Fatalf("unknown fields must be rejected instead of silently normalized: %#v", parsed.Envelope)
	}
}

func TestValidateRejectsConflictingPayloads(t *testing.T) {
	env := NewDataWithObservations("row", "ok", []Observation{{Kind: ObservationMetricValue}})
	env.Failure = &FailureRecord{QueryID: "fq_bad", Code: "BAD"}
	if err := env.Validate(); err == nil {
		t.Fatal("expected mutually exclusive payload validation error")
	}
}
