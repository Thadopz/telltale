package tools

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
)

func TestProjectRawEvaluationOutputRemovesEvidenceMetadata(t *testing.T) {
	output := projectRawEvaluationOutput(`{"success":true,"status":"success","evidence_id":"ev_private","can_use_as_evidence":true,"data":{"value":7},"trust_level":"trusted","observations":[{"kind":"metric_value"}],"scope":{"fields":{"service":"card-service"}},"summary":"seven"}`)
	for _, forbidden := range []string{"evidence_id", "can_use_as_evidence", "trust_level", "observations", "scope"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("raw projection leaked %s: %s", forbidden, output)
		}
	}
	for _, expected := range []string{`"status":"success"`, `"value":7`, `"summary":"seven"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("raw projection lost %s: %s", expected, output)
		}
	}
}

func TestIssueToolOutputRejectsLegacyEnvelope(t *testing.T) {
	raw := `{"success":true,"status":"success","evidence_id":"external-id","can_use_as_evidence":true,"data_status":"present","data":{"message":"untrusted document"}}`
	output := issueToolOutput(context.Background(), "query_internal_docs", raw)
	envelope := evidence.Parse(output).Envelope
	if envelope.Status != evidence.StatusProtocolError || envelope.Failure == nil {
		t.Fatalf("expected protocol gap, got %#v", envelope)
	}
	if envelope.DataStatus != evidence.DataUnknown || strings.TrimSpace(envelope.Failure.Reason) == "" {
		t.Fatalf("expected explicit protocol metadata, got %#v", envelope)
	}
}

func TestIssueToolOutputPreservesSQLResultMetadataAndReissuesEvidence(t *testing.T) {
	envelope := evidence.NewDataWithResultMetadata(
		map[string]any{"rows": []map[string]any{{"sql_digest": "select 1"}}},
		"bounded slow SQL",
		[]evidence.Observation{{
			Kind:   evidence.ObservationSlowSQL,
			Scope:  evidence.EvidenceScope{Fields: map[string]string{"sort_by": "avg_latency", "limit": "50"}},
			Fields: map[string]any{"sql_digest": "select 1"},
		}},
		evidence.ResultMetadata{
			ReturnedRows:     1,
			MaxRows:          50,
			ReturnedBytes:    128,
			MaxBytes:         64 * 1024,
			Truncated:        true,
			TruncationReason: "max_rows",
			SortBy:           "avg_latency",
		},
	)
	envelope.Evidence.EvidenceID = "external-id"
	output := issueToolOutput(context.Background(), "mysql_slow_sql", evidence.Marshal(envelope))
	issued := evidence.Parse(output).Envelope
	record, ok := issued.UsableEvidence()
	if !ok || record.EvidenceID == "external-id" {
		t.Fatalf("expected a locally issued evidence id, got %#v", issued)
	}
	if issued.DataStatus != evidence.DataPartial || !record.ResultMetadata.Truncated || record.ResultMetadata.MaxRows != 50 {
		t.Fatalf("expected SQL result metadata to survive local issuance, got %#v", issued)
	}
	if !strings.Contains(output, "result_metadata") {
		t.Fatalf("expected serialized result metadata, got %s", output)
	}
}
