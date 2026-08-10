package chat_pipeline

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"testing"

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
