package diagnosis

import (
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/input_preprocessor"
)

func TestValidateDiagnoseRequestRejectsResourceAbuse(t *testing.T) {
	_, err := validateDiagnoseRequest(DiagnoseRequest{
		Question: strings.Repeat("x", input_preprocessor.DefaultMaxQueryChars+1),
	})
	if err == nil {
		t.Fatal("expected oversized question to be rejected")
	}

	_, err = validateDiagnoseRequest(DiagnoseRequest{
		Question:       "check the service",
		ConversationID: "incident-1\r\nforged-log-entry",
	})
	if err == nil {
		t.Fatal("expected control characters in conversation_id to be rejected")
	}
}

func TestValidateDiagnoseRequestNormalizesControlCharactersInQuestion(t *testing.T) {
	request, err := validateDiagnoseRequest(DiagnoseRequest{
		Question:       " payment\x00\ncallback\tfailed ",
		ConversationID: "incident-1",
	})
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	if request.Question != "payment callback failed" {
		t.Fatalf("unexpected normalized question: %q", request.Question)
	}
}
