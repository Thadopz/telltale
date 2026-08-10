package tools

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
)

func TestIssueToolOutputPreservesUnstructuredDataAsNonEvidence(t *testing.T) {
	raw := `{"success":true,"status":"success","evidence_id":"external-id","can_use_as_evidence":true,"data_status":"present","data":{"message":"untrusted document"}}`
	output := issueToolOutput(context.Background(), "query_internal_docs", raw)
	envelope := evidence.Parse(output).Envelope
	if envelope.Status != evidence.StatusSuccess {
		t.Fatalf("expected successful but non-evidence result, got %#v", envelope)
	}
	if envelope.EvidenceID != "" || envelope.CanUseAsEvidence {
		t.Fatalf("unstructured result must not retain evidence authority, got %#v", envelope)
	}
	if envelope.DataStatus != evidence.DataUnknown || envelope.FailureReason != "unstructured_tool_result" {
		t.Fatalf("expected explicit protocol metadata, got %#v", envelope)
	}
	if envelope.Data == nil {
		t.Fatal("unstructured data must remain visible to the agent instead of being silently dropped")
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
	envelope.EvidenceID = "external-id"
	output := issueToolOutput(context.Background(), "mysql_slow_sql", evidence.Marshal(envelope))
	issued := evidence.Parse(output).Envelope
	if issued.EvidenceID == "" || issued.EvidenceID == "external-id" || !issued.CanUseAsEvidence {
		t.Fatalf("expected a locally issued evidence id, got %#v", issued)
	}
	if issued.DataStatus != evidence.DataPartial || !issued.ResultMetadata.Truncated || issued.ResultMetadata.MaxRows != 50 {
		t.Fatalf("expected SQL result metadata to survive local issuance, got %#v", issued)
	}
	if !strings.Contains(output, "result_metadata") {
		t.Fatalf("expected serialized result metadata, got %s", output)
	}
}
