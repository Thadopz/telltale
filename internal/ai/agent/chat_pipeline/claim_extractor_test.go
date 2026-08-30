package chat_pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fixedClaimExtractor struct {
	claimSet evidence.ClaimSet
	err      error
	input    ClaimExtractionInput
}

func (extractor *fixedClaimExtractor) Extract(_ context.Context, input ClaimExtractionInput) (evidence.ClaimSet, error) {
	extractor.input = input
	return extractor.claimSet, extractor.err
}

type claimModelStub struct {
	content string
}

func (modelStub claimModelStub) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(modelStub.content, nil), nil
}

func TestParseClaimSetAcceptsJSONCodeFence(t *testing.T) {
	claimSet, err := parseClaimSet("```json\n{\"schema_version\":1,\"claims\":[}\n```")
	if err == nil {
		t.Fatal("expected malformed JSON to fail")
	}
	if claimSet.SchemaVersion != 0 {
		t.Fatalf("expected empty claim set on parse error, got %#v", claimSet)
	}

	claimSet, err = parseClaimSet("```json\n{\"schema_version\":1,\"claims\":[]}\n```")
	if err != nil {
		t.Fatalf("expected fenced JSON to parse: %v", err)
	}
	if claimSet.SchemaVersion != evidence.ClaimSchemaVersion {
		t.Fatalf("unexpected schema version: %#v", claimSet)
	}
}

func TestModelClaimExtractorRejectsUnknownCoverageFields(t *testing.T) {
	extractor := NewModelClaimExtractor(claimModelStub{content: `{"schema_version":1,"claims":[],"status":"covered"}`})
	_, err := extractor.Extract(context.Background(), ClaimExtractionInput{})
	if err == nil {
		t.Fatal("expected unknown coverage field to be rejected")
	}
}

func TestParseClaimSetRemovesHarmlessClaimMetadataAndUnsupportedFields(t *testing.T) {
	raw := `{"schema_version":1,"claims":[{"claim_id":"c1","claim_type":"slow_sql","assertion_kind":"fact","observed_at":"2026-08-12T02:00:00Z","fields":{"duration_ms":2450,"slow_queries":17,"source_kind":"runtime_database"},"evidence_ids":["ev_1"]}]}`
	claimSet, err := parseClaimSet(raw)
	if err != nil {
		t.Fatalf("parse claim set: %v", err)
	}
	if len(claimSet.Claims) != 1 {
		t.Fatalf("unexpected claims: %#v", claimSet.Claims)
	}
	fields := claimSet.Claims[0].Fields
	if fields["duration_ms"] != float64(2450) {
		t.Fatalf("supported field was removed: %#v", fields)
	}
	if _, exists := fields["slow_queries"]; exists {
		t.Fatalf("unsupported field survived cleanup: %#v", fields)
	}
	if _, exists := fields["source_kind"]; exists {
		t.Fatalf("claim metadata survived cleanup: %#v", fields)
	}
}

func TestParseClaimSetStillRejectsTopLevelCoverageDecision(t *testing.T) {
	_, err := parseClaimSet(`{"schema_version":1,"claims":[],"coverage_status":"covered"}`)
	if err == nil {
		t.Fatal("expected top-level coverage decision to be rejected")
	}
}

func TestParseClaimSetDefaultsMissingSchemaVersion(t *testing.T) {
	claimSet, err := parseClaimSet(`{"claims":[{"claim_id":"c1","claim_type":"log_error","assertion_kind":"fact","evidence_ids":["ev_1"]}]}`)
	if err != nil {
		t.Fatalf("parse claim set without schema version: %v", err)
	}
	if claimSet.SchemaVersion != evidence.ClaimSchemaVersion || len(claimSet.Claims) != 1 {
		t.Fatalf("missing schema version was not safely defaulted: %#v", claimSet)
	}
}

func TestParseClaimSetStillRejectsExplicitUnsupportedSchemaVersion(t *testing.T) {
	_, err := parseClaimSet(`{"schema_version":2,"claims":[]}`)
	if err == nil {
		t.Fatal("explicit unsupported schema version must be rejected")
	}
}

func TestParseClaimSetDropsInventedClaimTypes(t *testing.T) {
	claimSet, err := parseClaimSet(`{"schema_version":1,"claims":[{"claim_id":"gap","claim_type":"evidence_gap","assertion_kind":"fact","evidence_ids":["ev_1"]},{"claim_id":"valid","claim_type":"log_error","assertion_kind":"fact","evidence_ids":["ev_1"]}]}`)
	if err != nil {
		t.Fatalf("parse claim set: %v", err)
	}
	if len(claimSet.Claims) != 1 || claimSet.Claims[0].ClaimID != "valid" {
		t.Fatalf("invented claim type was not dropped: %#v", claimSet.Claims)
	}
}

func TestParseClaimSetDropsClaimsWithInvalidAssertionForType(t *testing.T) {
	claimSet, err := parseClaimSet(`{"schema_version":1,"claims":[{"claim_id":"invalid","claim_type":"config_value_defined","assertion_kind":"fact","evidence_ids":["ev_1"]},{"claim_id":"valid","claim_type":"log_error","assertion_kind":"fact","evidence_ids":["ev_1"]}]}`)
	if err != nil {
		t.Fatalf("parse claim set: %v", err)
	}
	if len(claimSet.Claims) != 1 || claimSet.Claims[0].ClaimID != "valid" {
		t.Fatalf("claim with invalid assertion was not dropped: %#v", claimSet.Claims)
	}
}

func TestParseClaimSetCoercesExactScalarFieldsAndDropsInvalidOnes(t *testing.T) {
	raw := `{"schema_version":1,"claims":[{"claim_id":"c1","claim_type":"metric_anomaly","assertion_kind":"fact","fields":{"metric_value":"0.92","value":"stalled","service":42},"evidence_ids":["ev_1"]}]}`
	claimSet, err := parseClaimSet(raw)
	if err != nil {
		t.Fatalf("parse claim set: %v", err)
	}
	fields := claimSet.Claims[0].Fields
	if fields["metric_value"] != float64(0.92) {
		t.Fatalf("quoted number was not normalized: %#v", fields)
	}
	if _, exists := fields["value"]; exists {
		t.Fatalf("non-numeric value survived cleanup: %#v", fields)
	}
	if _, exists := fields["service"]; exists {
		t.Fatalf("non-string service survived cleanup: %#v", fields)
	}
}

func TestClaimExtractionPromptListsRegisteredSchema(t *testing.T) {
	prompt := claimExtractionPrompt()
	if !strings.Contains(prompt, "- slow_sql;") || !strings.Contains(prompt, "duration_ms:number") {
		t.Fatalf("claim prompt does not describe registered schema: %s", prompt)
	}
}

func TestModelClaimExtractorReturnsStructuredClaimWithoutReadingNaturalLanguageAnswer(t *testing.T) {
	content := `{"schema_version":1,"claims":[{"claim_id":"c1","claim_type":"log_error","assertion_kind":"fact","evidence_ids":["ev_1"]}]}`
	extractor := NewModelClaimExtractor(claimModelStub{content: content})
	claimSet, err := extractor.Extract(context.Background(), ClaimExtractionInput{DraftAnswer: "payment timeout in any language"})
	if err != nil {
		t.Fatalf("extract claim set: %v", err)
	}
	if len(claimSet.Claims) != 1 || claimSet.Claims[0].EvidenceIDs[0] != "ev_1" {
		t.Fatalf("unexpected structured claim set: %#v", claimSet)
	}
}
