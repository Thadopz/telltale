package evidence

import (
	"context"
	"testing"
)

func TestRecordRuntimeAnchorsKeepsStableSafeValuesOnly(t *testing.T) {
	store := NewAnchorStore()
	ctx := WithAnchorStore(context.Background(), store)
	env := NewDataWithObservations(nil, "runtime log", []Observation{{
		Kind:    ObservationLogError,
		Subject: SubjectRef{Service: "payment", Fields: map[string]string{"password": "super-secret"}},
		Fields: map[string]any{
			"error_code":        "ERR_CALLBACK_TIMEOUT",
			"route":             "/payments/callback",
			"message":           "callback timeout",
			"metric_name":       "payment_failed_rate",
			"authorization":     "Bearer very-secret-token",
			"connection_string": "postgres://user:password@db/cards",
		},
	}})
	env.Evidence.EvidenceID = "ev_runtime"
	env.Evidence.Provenance.SourceKind = SourceRuntimeLog
	RecordRuntimeAnchors(ctx, env)

	for kind, value := range map[string]string{
		"error_code":        "ERR_CALLBACK_TIMEOUT",
		"route":             "/payments/callback",
		"fixed_log_message": "callback timeout",
		"service_name":      "payment",
	} {
		if _, ok := store.Get(anchorID(env.Evidence.EvidenceID, kind, value)); !ok {
			t.Fatalf("expected safe %s anchor for %q", kind, value)
		}
	}
	if _, ok := store.Get(anchorID(env.Evidence.EvidenceID, "runtime_identifier", "payment_failed_rate")); ok {
		t.Fatal("unexpected generic runtime identifier anchor")
	}
}

func TestRecordRuntimeAnchorsIgnoresStaticEvidence(t *testing.T) {
	store := NewAnchorStore()
	env := NewDataWithObservations(nil, "static code", []Observation{{Kind: ObservationStaticCodeDeclaration, Fields: map[string]any{"function": "OpenPool"}}})
	env.Evidence.EvidenceID = "ev_static"
	env.Evidence.Provenance.SourceKind = SourceStaticCode
	RecordRuntimeAnchors(WithAnchorStore(context.Background(), store), env)
	if _, ok := store.Get(anchorID("ev_static", "function", "OpenPool")); ok {
		t.Fatal("static source must not create runtime anchors")
	}
}
