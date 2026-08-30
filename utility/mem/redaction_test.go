package mem

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
)

func TestRedactJSONRemovesCredentialValues(t *testing.T) {
	raw := `{"user":"app","password":"super-secret-password","authorization":"Bearer abcdefghijklmnop","nested":{"api_key":"key-value"}}`
	redacted := redactJSON(raw)
	for _, secret := range []string{"super-secret-password", "abcdefghijklmnop", "key-value"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted JSON contains secret %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "redacted") {
		t.Fatalf("expected redaction marker, got %s", redacted)
	}
}

func TestRedactTextHandlesDSNBearerPEMAndHighEntropyValues(t *testing.T) {
	raw := "mysql://root:password@db.example/orders bearer abcdefghijklmnop -----BEGIN PRIVATE KEY-----secret-----END PRIVATE KEY----- token=0123456789abcdef0123456789abcdef nonce abcdefghijklmnopqrstuvwxyz123456"
	redacted := redactText(raw)
	for _, secret := range []string{"root:password", "abcdefghijklmnop", "BEGIN PRIVATE KEY-----secret", "0123456789abcdef0123456789abcdef", "abcdefghijklmnopqrstuvwxyz123456"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted text contains secret fragment %q: %s", secret, redacted)
		}
	}
	for _, marker := range []string{"<redacted:dsn>", "<redacted:token>", "<redacted:pem>", "<redacted:high-entropy>"} {
		if !strings.Contains(redacted, marker) {
			t.Fatalf("expected marker %q in %s", marker, redacted)
		}
	}
}

func TestRedactToolOutputDropsRawDataAndSanitizesObservations(t *testing.T) {
	envelope := evidence.NewDataWithObservations(map[string]any{"password": "do-not-persist"}, "authorization=do-not-persist", []evidence.Observation{{
		Kind:    evidence.ObservationLogError,
		Fields:  map[string]any{"token": "do-not-persist-token", "error_code": "ERR_PAYMENT"},
		Summary: "password=do-not-persist",
	}})
	envelope.Evidence.EvidenceID = "ev_local"
	envelope.Evidence.Provenance.SourceKind = evidence.SourceRuntimeLog
	raw := evidence.Marshal(envelope)

	redacted := redactToolOutput(raw)
	if redacted == "" {
		t.Fatal("expected sanitized evidence envelope")
	}
	for _, secret := range []string{"do-not-persist", "do-not-persist-token"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("persisted tool output contains secret %q: %s", secret, redacted)
		}
	}
	parsed := evidence.Parse(redacted).Envelope
	if parsed.Data != nil {
		t.Fatalf("raw tool data must not be persisted: %#v", parsed.Data)
	}
	if parsed.Evidence == nil || len(parsed.Evidence.Observations) != 1 || parsed.Evidence.Observations[0].Fields["token"] == "do-not-persist-token" {
		t.Fatalf("observation fields were not sanitized: %#v", parsed.Evidence)
	}
}

func TestRedactToolOutputRejectsRawUnstructuredOutput(t *testing.T) {
	if got := redactToolOutput("raw output with password=secret"); got != "" {
		t.Fatalf("expected raw unstructured output to be dropped, got %q", got)
	}
}
