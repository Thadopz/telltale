package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func configureLogSources(t *testing.T, sources map[string]logSource, defaultID string) {
	t.Helper()
	previous := g.Cfg().GetAdapter()
	content, err := json.Marshal(map[string]any{"tools": map[string]any{"logs": map[string]any{"sources": sources, "default_source": defaultID}}, "permissions": map[string]any{"enabled": true, "roles": map[string]any{
		"log_only":     map[string]any{"scopes": []string{string(authz.PermLogsRead)}},
		"payment_only": map[string]any{"scopes": []string{string(authz.PermPaymentRead)}},
		"full":         map[string]any{"scopes": []string{string(authz.PermLogsRead), string(authz.PermPaymentRead), "tool:inventory.logs.read", "tool:secondary.logs.read"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := gcfg.NewAdapterContent(string(content))
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
}

func configureTestLogSource(t *testing.T, endpoint string) {
	t.Helper()
	configureLogSources(t, map[string]logSource{"diagnostic_logs": {URL: endpoint, Permission: authz.PermPaymentRead, Services: map[string]authz.Permission{"payment": authz.PermPaymentRead}}}, "diagnostic_logs")
}

func registryLogTool(t *testing.T, ctx context.Context, name string) tool.InvokableTool {
	t.Helper()
	list, err := NewRegistry().ToolsForAgent(ctx, ScenarioChat, authz.AgentScopeObservability)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range list {
		info, _ := candidate.Info(ctx)
		if info.Name == name {
			return candidate.(tool.InvokableTool)
		}
	}
	t.Fatalf("registry missing %s", name)
	return nil
}

func testLogData(service, start, end, marker string) map[string]any {
	s, _ := time.Parse(time.RFC3339, start)
	e, _ := time.Parse(time.RFC3339, end)
	return map[string]any{"service": service, "start": start, "end": end, "baseline_start": s.Add(-e.Sub(s)).Format(time.RFC3339Nano), "baseline_end": start, "count_unit": "log_event", "statistics_coverage": "complete_within_query", "samples_coverage": "partial_finite_window", "totals": map[string]any{"events": 100000, "errors": 20000}, "baseline_totals": map[string]any{"events": 100000, "errors": 0}, "candidates": []any{map[string]any{"operation": "shared", "event_error_share_delta": 0.2}}, "samples": []any{map[string]any{"id": "1", "operation": "shared", "level": "ERROR", "message": marker, "occurred_at": "2026-10-14 10:00:00.123456"}, map[string]any{"id": "2", "operation": "shared", "level": "INFO", "message": "control", "occurred_at": "2026-10-14 10:00:00.123456"}}, "limitations": []any{"No trace backend."}}
}

func TestLogSourcesRouteServicesAndReauthorizeReferences(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	backend := func(marker string, direct bool, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			service := r.URL.Query().Get("service")
			var data any
			if r.URL.Path == "/api/v1/agent/analyze_log_window" {
				data = testLogData(service, r.URL.Query().Get("start"), r.URL.Query().Get("end"), marker+":"+service)
			} else {
				data = map[string]any{"id": r.URL.Query().Get("id"), "service": service, "operation": "shared", "level": "ERROR", "message": marker + ":" + service, "occurred_at": "2026-10-14 10:00:00.123456"}
			}
			if !direct {
				data = map[string]any{"code": 0, "data": data}
			}
			_ = json.NewEncoder(w).Encode(data)
		}))
	}
	first := backend("first", false, &firstCalls)
	defer first.Close()
	second := backend("second", true, &secondCalls)
	defer second.Close()
	sources := map[string]logSource{
		"first":  {URL: first.URL, Permission: authz.PermPaymentRead, Services: map[string]authz.Permission{"payment": authz.PermPaymentRead, "inventory": "tool:inventory.logs.read"}},
		"second": {URL: second.URL, ResponseFormat: "direct", Permission: "tool:secondary.logs.read", Services: map[string]authz.Permission{"payment": authz.PermPaymentRead, "inventory": "tool:inventory.logs.read"}},
	}
	configureLogSources(t, sources, "")
	ctx := contextbudget.WithToolBudget(authz.WithPrincipal(logTestContext(t), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{"full"}}))
	analyze := registryLogTool(t, ctx, "analyze_log_window")
	read := registryLogTool(t, ctx, "read_log_evidence")
	refs := map[string]string{}
	for _, scope := range []struct{ source, service string }{{"first", "payment"}, {"first", "inventory"}, {"second", "payment"}} {
		args, _ := json.Marshal(AnalyzeLogWindowInput{SourceID: scope.source, Service: scope.service, Start: "2026-10-14T10:00:00.1234567+08:00", End: "2026-10-14T10:10:00.1234567+08:00"})
		raw, err := analyze.InvokableRun(ctx, string(args))
		if err != nil {
			t.Fatal(err)
		}
		env := evidence.Parse(raw).Envelope
		if env.Status != evidence.StatusSuccess {
			t.Fatalf("analyze: %s", raw)
		}
		data := env.Data.(map[string]any)
		if data["source_id"] != scope.source || data["service"] != scope.service || env.Evidence.Observations[0].Subject.Service != scope.service || env.Evidence.Observations[0].Subject.Fields["source_id"] != scope.source {
			t.Fatal("source/service evidence mismatch")
		}
		ref := data["samples"].([]any)[0].(map[string]any)["reference"].(string)
		refs[scope.source+":"+scope.service] = ref
		readArgs, _ := json.Marshal(ReadLogEvidenceInput{Reference: ref})
		readRaw, _ := read.InvokableRun(ctx, string(readArgs))
		detail := evidence.Parse(readRaw).Envelope
		if detail.Status != evidence.StatusSuccess {
			t.Fatalf("read: %s", readRaw)
		}
		got := detail.Data.(map[string]any)
		if got["message"] != scope.source+":"+scope.service || got["source_id"] != scope.source || got["changed_since_sample"] != false {
			t.Fatalf("cross-source/service reread: %v", got)
		}
	}
	if firstCalls.Load() != 4 || secondCalls.Load() != 2 {
		t.Fatalf("routing calls first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	for _, bad := range []AnalyzeLogWindowInput{{Service: "payment", Start: "2026-10-14T10:00:00Z", End: "2026-10-14T10:01:00Z"}, {SourceID: "missing", Service: "payment", Start: "2026-10-14T10:00:00Z", End: "2026-10-14T10:01:00Z"}, {SourceID: "first", Service: "unknown", Start: "2026-10-14T10:00:00Z", End: "2026-10-14T10:01:00Z"}} {
		args, _ := json.Marshal(bad)
		raw, _ := analyze.InvokableRun(ctx, string(args))
		if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
			t.Fatal("invalid source/service accepted")
		}
	}
	for _, role := range []string{"log_only", "payment_only"} {
		denied := authz.WithPrincipal(logTestContext(t), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{role}})
		raw, _ := read.InvokableRun(denied, `{"reference":"`+refs["first:payment"]+`"}`)
		if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
			t.Fatal("old reference bypassed revoked user permissions")
		}
	}
	serviceDenied := authz.WithPrincipal(logTestContext(t), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{"oncall"}})
	deniedResult, _ := analyze.InvokableRun(serviceDenied, `{"source_id":"first","service":"inventory","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:01:00Z"}`)
	if evidence.Parse(deniedResult).Envelope.Status == evidence.StatusSuccess {
		t.Fatal("source allowlist substituted for service permission")
	}
	for _, role := range []string{"log_only", "payment_only"} {
		denied := authz.WithPrincipal(logTestContext(t), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{role}})
		raw, _ := analyze.InvokableRun(denied, `{"source_id":"first","service":"payment","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:01:00Z"}`)
		if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
			t.Fatal("source permission widened existing payment access")
		}
	}
	configureLogSources(t, sources, "first")
	repeated, _ := analyze.InvokableRun(ctx, `{"service":"payment","start":"2026-10-14T02:00:00.1234567Z","end":"2026-10-14T02:10:00.1234567Z"}`)
	if env := evidence.Parse(repeated).Envelope; env.Failure == nil || env.Failure.Code != "LOG_CHECK_ALREADY_USED" {
		t.Fatalf("default source or timezone alias bypassed repetition guard: %s", repeated)
	}
	changed := sources["first"]
	changed.URL = second.URL
	sources["first"] = changed
	configureLogSources(t, sources, "first")
	raw, _ := read.InvokableRun(ctx, `{"reference":"`+refs["first:payment"]+`"}`)
	if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
		t.Fatal("old reference redirected to replacement backend")
	}
	changed.URL = first.URL
	sources["first"] = changed
	delete(sources["first"].Services, "payment")
	configureLogSources(t, sources, "")
	raw, _ = read.InvokableRun(logTestContext(t), `{"reference":"`+refs["first:payment"]+`"}`)
	if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
		t.Fatal("service revocation did not invalidate old reference")
	}
	if firstCalls.Load() != 4 || secondCalls.Load() != 2 {
		t.Fatal("denied query reached a backend")
	}
}

func TestLogMissingCapabilitiesKeepStatisticsWithoutFakeReferences(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		data := testLogData("inventory", r.URL.Query().Get("start"), r.URL.Query().Get("end"), "sample")
		data["samples"].([]any)[0].(map[string]any)["reference"] = "backend_fake_ref"
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer server.Close()
	sources := map[string]logSource{"inventory_logs": {URL: server.URL, ResponseFormat: "direct", Services: map[string]authz.Permission{"inventory": "tool:inventory.logs.read"}, Capabilities: &logCapabilities{Aggregation: true, Dimensions: []string{"level"}}}}
	configureLogSources(t, sources, "")
	ctx := authz.WithPrincipal(logTestContext(t), authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{"full"}})
	analyze := registryLogTool(t, ctx, "analyze_log_window")
	raw, _ := analyze.InvokableRun(ctx, `{"service":"inventory","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:10:00Z"}`)
	env := evidence.Parse(raw).Envelope
	if env.Status != evidence.StatusSuccess {
		t.Fatalf("limited capability query: %s", raw)
	}
	data := env.Data.(map[string]any)
	if data["source_id"] != "inventory_logs" || data["baseline_totals"] != nil || data["baseline_coverage"] != "unavailable" || data["totals"].(map[string]any)["events"] != float64(100000) {
		t.Fatal("capability projection lost statistics or invented baseline")
	}
	for _, value := range data["samples"].([]any) {
		if value.(map[string]any)["reference"] != nil {
			t.Fatal("unreadable sample received a reference")
		}
	}
	if data["candidates"].([]any)[0].(map[string]any)["event_error_share_delta"] != nil {
		t.Fatal("unsupported baseline comparison exposed")
	}
	for _, args := range []string{`{"service":"inventory","operation":"shared","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:10:00Z"}`, `{"source_id":"unknown","service":"inventory","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:10:00Z"}`} {
		raw, _ := analyze.InvokableRun(ctx, args)
		if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
			t.Fatal("missing capability or source accepted")
		}
	}
	sources["inventory_logs"].Capabilities.Aggregation = false
	configureLogSources(t, sources, "")
	raw, _ = analyze.InvokableRun(ctx, `{"service":"inventory","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:10:00Z"}`)
	if env := evidence.Parse(raw).Envelope; env.Failure == nil || env.Failure.Code != "CAPABILITY_NOT_SUPPORTED" || calls.Load() != 1 {
		t.Fatal("unsupported aggregation performed a backend query")
	}
}

func TestObservabilityRegistryPreservesBusinessPermissions(t *testing.T) {
	ctx := logTestContext(t)
	registry := NewRegistry()
	observability, err := registry.ToolsForAgent(ctx, ScenarioChat, authz.AgentScopeObservability)
	if err != nil {
		t.Fatal(err)
	}
	database, err := registry.ToolsForAgent(ctx, ScenarioChat, authz.AgentScopeDatabase)
	if err != nil {
		t.Fatal(err)
	}
	obsNames, databaseNames := toolNames(t, ctx, observability), toolNames(t, ctx, database)
	for _, name := range []string{"analyze_log_window", "read_log_evidence"} {
		if !obsNames[name] || databaseNames[name] {
			t.Fatal("generic log tool crossed functional boundary")
		}
	}
	for _, name := range []string{"search_payment_logs", "query_payment_order_detail", "query_payment_metrics", "check_payment_consistency"} {
		if !obsNames[name] || databaseNames[name] {
			t.Fatal("business observations must belong to observability")
		}
	}
	analyze := registryLogTool(t, ctx, "analyze_log_window")
	raw, _ := analyze.InvokableRun(authz.WithAgentScope(ctx, authz.AgentScopeDatabase), `{}`)
	if evidence.Parse(raw).Envelope.Status == evidence.StatusSuccess {
		t.Fatal("database scope bypassed generic tool scope")
	}
	readOnlyLogs := authz.WithPrincipal(ctx, authz.Principal{UserID: "ops", TenantID: "tenant", Roles: []string{"sregym"}})
	limited, err := NewRegistry().ToolsForAgent(readOnlyLogs, ScenarioChat, authz.AgentScopeObservability)
	if err != nil {
		t.Fatal(err)
	}
	if toolNames(t, readOnlyLogs, limited)["query_payment_metrics"] {
		t.Fatal("observability routing granted payment permission")
	}
	for _, candidate := range observability {
		info, _ := candidate.Info(ctx)
		if info.Name == "query_payment_metrics" {
			output, err := candidate.(tool.InvokableTool).InvokableRun(readOnlyLogs, `{}`)
			if err != nil || !strings.Contains(output, "missing permission tool:payment.read") {
				t.Fatalf("business query bypassed payment permission: %v %s", err, output)
			}
		}
	}
}

func TestDiagnosticLocatorRemainsOpaqueOutsideDatabaseAdapter(t *testing.T) {
	locator, err := (logSource{ResponseFormat: "direct"}).locator(map[string]any{"id": "record:opaque/1"})
	if err != nil || locator != "record:opaque/1" {
		t.Fatal("public source boundary assumed a numeric row ID")
	}
	if _, err := (logSource{ResponseFormat: "goframe"}).locator(map[string]any{"id": "record:opaque/1"}); err == nil {
		t.Fatal("database adapter accepted invalid numeric ID")
	}
}
