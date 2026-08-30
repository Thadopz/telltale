package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestModelEvidenceViewOmitsAuditIdentity(t *testing.T) {
	now := time.Now().UTC()
	item := EvidenceItem{
		EvidenceID: "ev_model", ToolName: "query_logs", SourceKind: SourceRuntimeLog,
		RuntimeVerified: true, Issuer: "tool:query_logs", RunID: "run-secret",
		ConversationID: "conversation-secret", TenantID: "tenant-secret",
		ContentDigest: "digest-secret", IssuerTrust: ContentTrustTrustedLocal,
		ObservedAt: now, Observations: []Observation{{Kind: ObservationLogError}},
	}
	encoded, err := json.Marshal(item.ModelView())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	full, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(full) {
		t.Fatalf("model projection did not reduce payload: full=%d view=%d", len(full), len(encoded))
	}
	for _, forbidden := range []string{"run-secret", "conversation-secret", "tenant-secret", "digest-secret", "issuer_trust"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("model view leaked audit field %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"ev_model", "query_logs", "runtime_log", "log_error"} {
		if !strings.Contains(text, required) {
			t.Fatalf("model view omitted required evidence field %q: %s", required, text)
		}
	}
}
