package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// RecordRuntimeAnchors extracts only stable identifiers from a successful
// runtime envelope. The extracted values help code_search stay anchored to
// live evidence instead of broad repository browsing.
func RecordRuntimeAnchors(ctx context.Context, env Envelope) {
	record, ok := env.UsableEvidence()
	if !ok {
		return
	}
	if record.Provenance.SourceKind == SourceStaticCode || record.Provenance.SourceKind == SourceStaticConfig {
		return
	}
	store := AnchorStoreFromContext(ctx)
	if store == nil {
		return
	}

	addAnchor := func(kind string, value string, confidence float64) {
		value = strings.TrimSpace(value)
		if kind == "" || !safeAnchorValue(kind, value) {
			return
		}
		store.Add(CodeAnchor{
			ID:               anchorID(record.EvidenceID, kind, value),
			Kind:             kind,
			Value:            value,
			SourceEvidenceID: record.EvidenceID,
			Confidence:       confidence,
		})
	}

	for _, observation := range record.Observations {
		for key, value := range observation.Fields {
			addAnchor(anchorKind(key), fmt.Sprint(value), 0.85)
		}
		addAnchor("service_name", observation.Subject.Service, 0.95)
		addAnchor("order_no", observation.Subject.OrderNo, 0.95)
		addAnchor("trace_id", observation.Subject.TraceID, 0.95)
	}
}

func anchorID(evidenceID string, kind string, value string) string {
	sum := sha256.Sum256([]byte(evidenceID + "\x00" + kind + "\x00" + value))
	return "anchor_" + hex.EncodeToString(sum[:8])
}

func anchorKind(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "error", "error_code", "err_code", "code":
		return "error_code"
	case "route", "path", "request_path", "url":
		return "route"
	case "function", "func", "method", "symbol":
		return "function"
	case "sql", "sql_digest", "sql_text", "digest":
		return "sql_digest"
	case "config", "config_key", "configuration":
		return "config_key"
	case "service", "service_name":
		return "service_name"
	case "message", "log_message", "fixed_log_message":
		return "fixed_log_message"
	default:
		return ""
	}
}

func safeAnchorValue(kind string, value string) bool {
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "bearer ", "private_key", "://"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if kind == "fixed_log_message" && len(value) > 160 {
		return false
	}
	return true
}
