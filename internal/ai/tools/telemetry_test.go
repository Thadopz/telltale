package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/mem"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func telemetryTestScope(t *testing.T, kind ...string) (context.Context, telemetrySource, TelemetryQuery) {
	t.Helper()
	t.Setenv("TELLTALE_TELEMETRY_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	s := telemetrySource{
		Kind: "loki", URL: "http://127.0.0.1:3100", Tenant: "tenant",
		Environment: "test", ServiceLabel: "service", EnvironmentLabel: "environment",
		Services: map[string]authz.Permission{"inventory": authz.PermLogsRead},
	}
	if len(kind) > 0 {
		s.Kind = kind[0]
	}
	config, err := json.Marshal(map[string]any{"tools": map[string]any{"telemetry": map[string]any{"sources": map[string]telemetrySource{"logs": s}}}})
	if err != nil {
		t.Fatal(err)
	}
	previous := g.Cfg().GetAdapter()
	adapter, err := gcfg.NewAdapterContent(string(config))
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	t.Cleanup(func() {
		g.Cfg().SetAdapter(previous)
	})
	ctx := authz.WithPrincipal(t.Context(), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{"oncall"}})
	ctx = authz.WithAgentScope(ctx, authz.AgentScopeObservability)
	q := TelemetryQuery{Source: "logs", Environment: "test", Service: "inventory", Start: "2026-11-14T00:00:00Z", End: "2026-11-14T00:10:00Z", Limit: 2}
	return ctx, s, q
}

func TestMetricDiscoveryRequestsAreScopedAndBounded(t *testing.T) {
	_, s, q := telemetryTestScope(t)
	q.Limit = 5
	for mode, path := range map[string]string{
		"names":  "/api/v1/label/__name__/values",
		"labels": "/api/v1/labels",
		"values": "/api/v1/label/instance/values",
	} {
		input := DiscoverMetricsInput{Query: q, Mode: mode, Label: "instance"}
		got, values, err := metricDiscoveryRequest(s, &input)
		if err != nil || got != path || values.Get("limit") != "6" || !strings.Contains(values.Get("match[]"), `service="inventory"`) || !strings.Contains(values.Get("match[]"), `environment="test"`) || values.Get("start") == "" || values.Get("end") == "" {
			t.Fatalf("unscoped discovery request: %s %v %v", got, values, err)
		}
	}
	q.Labels = map[string]string{"service": "outside"}
	if _, _, err := metricDiscoveryRequest(s, &DiscoverMetricsInput{Query: q, Mode: "names"}); err == nil {
		t.Fatal("scope override accepted")
	}
	q.Labels = nil
	if _, _, err := metricDiscoveryRequest(s, &DiscoverMetricsInput{Query: q, Mode: "values", Label: "../secret"}); err == nil {
		t.Fatal("invalid label accepted")
	}
}

func TestTracePagesReassembleAndSurviveOutputBudget(t *testing.T) {
	ctx, s, q := telemetryTestScope(t, "tempo")
	// Exercise the actual paging and output algorithms without impersonating a backend response.
	trace := map[string]any{"description": strings.Repeat("调用链🙂", 1500), "password": "private-value"}
	loc := telemetryLocator{Kind: "tempo", Query: q, TraceID: strings.Repeat("a", 32)}
	var joined strings.Builder
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("pagination did not terminate")
		}
		out, err := traceTelemetryPage(ctx, s, loc, trace, 512)
		if err != nil {
			t.Fatal(err)
		}
		issued := IssueToolOutput(ctx, "read_evidence", marshalTelemetryData(ctx, out, "trace fragment"))
		env := evidence.Parse(issued).Envelope
		if env.Status != evidence.StatusSuccess {
			t.Fatalf("page failed: %s", issued)
		}
		data := env.Data.(map[string]any)
		fragment, ok := data["records"].(string)
		if !ok || fragment == "" {
			t.Fatal("page omitted")
		}
		joined.WriteString(fragment)
		meta := data["telemetry"].(map[string]any)
		next, _ := meta["next_cursor"].(string)
		if next == "" {
			break
		}
		loc, _, err = openTelemetry(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected, _ := json.Marshal(evidence.SanitizeUntrustedValue(mem.RedactToolPayload(trace)))
	if joined.String() != string(expected) || strings.Contains(joined.String(), "private-value") {
		t.Fatal("trace pages lost content, duplicated content or leaked secret")
	}
	loc.Offset = 0
	loc.Snapshot = "changed"
	if _, err := traceTelemetryPage(ctx, s, loc, trace, 512); err == nil {
		t.Fatal("changed snapshot accepted")
	}
	loc.Snapshot = ""
	limited := contextbudget.WithToolAllocation(ctx, contextbudget.ToolAllocation{TotalTokens: 10000, OutputTokens: 2000})
	out, err := traceTelemetryPage(ctx, s, loc, trace, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issued := IssueToolOutput(limited, "read_evidence", marshalTelemetryData(limited, out, "trace fragment"))
	env := evidence.Parse(issued).Envelope
	if env.Status != evidence.StatusSuccess || env.DataStatus != evidence.DataPartial {
		t.Fatalf("expected bounded partial page: %s", issued)
	}
	data := env.Data.(map[string]any)
	meta := data["telemetry"].(map[string]any)
	retry := meta["next_cursor"].(string)
	loc, _, err = openTelemetry(ctx, retry)
	if err != nil || loc.Offset != 0 {
		t.Fatalf("budget retry skipped content: %v", err)
	}
	out, err = traceTelemetryPage(ctx, s, loc, trace, 64)
	if err != nil {
		t.Fatal(err)
	}
	issued = IssueToolOutput(limited, "read_evidence", marshalTelemetryData(limited, out, "trace fragment"))
	env = evidence.Parse(issued).Envelope
	if env.Status != evidence.StatusSuccess || env.DataStatus == evidence.DataPartial {
		t.Fatalf("smaller retry page failed: %s", issued)
	}
}

func TestTelemetryLocatorAuthenticationAndScope(t *testing.T) {
	ctx, s, q := telemetryTestScope(t)
	ref, err := sealTelemetry(ctx, s, telemetryLocator{Query: q, Kind: "loki", Offset: 2, Snapshot: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	loc, _, err := openTelemetry(ctx, ref)
	if err != nil || loc.Offset != 2 || loc.Query.Service != q.Service {
		t.Fatalf("locator roundtrip: %v", err)
	}
	for _, other := range []context.Context{
		authz.WithPrincipal(ctx, authz.Principal{UserID: "other", TenantID: "tenant", Roles: []string{"oncall"}}),
		authz.WithPrincipal(ctx, authz.Principal{UserID: "ops", TenantID: "other", Roles: []string{"oncall"}}),
		context.WithValue(ctx, "client_id", "other-session"),
		authz.WithAgentScope(ctx, authz.AgentScopeKnowledge),
	} {
		if _, _, err := openTelemetry(other, ref); err == nil {
			t.Fatal("cross-scope locator accepted")
		}
	}
	b, _ := base64.RawURLEncoding.DecodeString(ref)
	b[len(b)-1] ^= 1
	if _, _, err := openTelemetry(ctx, base64.RawURLEncoding.EncodeToString(b)); err == nil {
		t.Fatal("tampered locator accepted")
	}
	if _, err := resolveTelemetry(ctx, TelemetryQuery{Source: "logs", Service: "other", Environment: "test"}, "loki"); err == nil {
		t.Fatal("unauthorized service accepted")
	}
	// A past expiry is authenticated with the real cipher to exercise expiration independently of tampering.
	aead, _ := telemetryAEAD()
	loc.Expires = time.Now().Add(-time.Second).Unix()
	plain, _ := json.Marshal(loc)
	nonce := make([]byte, aead.NonceSize())
	expired := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte("telltale-telemetry-v1")))
	if _, _, err := openTelemetry(ctx, expired); err == nil {
		t.Fatal("expired locator accepted")
	}
	t.Setenv("TELLTALE_TELEMETRY_KEY", "")
	if _, err := sealTelemetry(ctx, s, loc); err == nil {
		t.Fatal("missing secret accepted")
	}
}

func TestTelemetryOutputPreservesAuthenticatedContinuation(t *testing.T) {
	ctx, s, q := telemetryTestScope(t)
	ref, err := sealTelemetry(ctx, s, telemetryLocator{Query: q, Kind: "loki", Snapshot: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	output := marshalToolData(map[string]any{
		"telemetry": map[string]any{"next_cursor": ref, "retry_cursor": ref, "returned": 1, "matched": "unknown", "scope": q},
		"records":   []any{map[string]any{"evidence_ref": ref, "line": strings.Repeat("sensitive log line ", 10000)}},
	}, "bounded telemetry")
	issued := IssueToolOutput(ctx, "search_logs", output)
	parsed := evidence.Parse(issued)
	if parsed.Envelope.Status != evidence.StatusSuccess {
		t.Fatalf("projection failed: %s", issued)
	}
	data, ok := parsed.Envelope.Data.(map[string]any)
	if !ok {
		t.Fatal("missing data")
	}
	metadata := data["telemetry"].(map[string]any)
	cursor, _ := metadata["next_cursor"].(string)
	if cursor != ref {
		t.Fatalf("cursor corrupted by output processing: %q", cursor)
	}
	if _, _, err := openTelemetry(ctx, cursor); err != nil {
		t.Fatal(err)
	}
	if contextbudget.EstimateTextTokens(issued) > contextbudget.ToolLimits(ctx).OutputTokens {
		t.Fatal("issued output exceeds budget")
	}
}

func TestTelemetryRangeSelectorAndAlertInstances(t *testing.T) {
	_, s, q := telemetryTestScope(t)
	q.End = "2026-11-16T00:00:00Z"
	if _, _, err := telemetryRange(&q); err == nil {
		t.Fatal("unbounded range accepted")
	}
	q.Labels = map[string]string{"service": "outside"}
	if _, err := telemetrySelector(s, q); err == nil {
		t.Fatal("selector scope override accepted")
	}
	alerts := []PrometheusAlert{
		{Labels: map[string]string{"alertname": "Errors", "instance": "a"}},
		{Labels: map[string]string{"alertname": "Errors", "instance": "b"}},
	}
	if got := simplifyPrometheusAlerts(alerts, prometheusAlertFilter{}); len(got) != 2 {
		t.Fatal("different alert instances collapsed")
	}
}
