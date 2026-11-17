package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

type logRoutingModel struct {
	tools []*schema.ToolInfo
	calls *atomic.Int32
}

func (m *logRoutingModel) WithTools(list []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copy := *m
	copy.tools = list
	return &copy, nil
}

func (m *logRoutingModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.calls.Add(1)
	coordinator := false
	list := m.tools
	if options := model.GetCommonOptions(nil, opts...); options != nil {
		list = append(append([]*schema.ToolInfo(nil), list...), options.Tools...)
	}
	for _, info := range list {
		if info.Name == "observability_agent" {
			coordinator = true
		}
	}
	var latest *schema.Message
	for _, message := range messages {
		if message != nil && message.Role == schema.Tool {
			latest = message
		}
	}
	call := func(name, args string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{{ID: name, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}})
	}
	if coordinator {
		if latest != nil {
			return schema.AssistantMessage("inventory diagnosis complete", nil), nil
		}
		return call("observability_agent", `{"request":"Analyze inventory_logs / inventory during 2026-10-14T10:00:00Z to 2026-10-14T10:10:00Z, then verify a sample."}`), nil
	}
	if authz.AgentScopeFromContext(ctx) != authz.AgentScopeObservability {
		return nil, fmt.Errorf("generic logs executed outside observability scope")
	}
	if latest == nil {
		return call("analyze_log_window", `{"source_id":"inventory_logs","service":"inventory","start":"2026-10-14T10:00:00Z","end":"2026-10-14T10:10:00Z"}`), nil
	}
	env := evidence.Parse(latest.Content).Envelope
	if env.Status != evidence.StatusSuccess || env.Evidence == nil || env.Evidence.Observations[0].Subject.Service != "inventory" {
		return nil, fmt.Errorf("missing scoped inventory evidence: %s", latest.Content)
	}
	if latest.ToolName == "analyze_log_window" {
		data := env.Data.(map[string]any)
		ref := data["samples"].([]any)[0].(map[string]any)["reference"]
		args, _ := json.Marshal(map[string]any{"reference": ref})
		return call("read_log_evidence", string(args)), nil
	}
	return schema.AssistantMessage("inventory evidence "+env.Evidence.EvidenceID+"; source inventory_logs", nil), nil
}

func (m *logRoutingModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func TestCoordinatorRoutesGenericLogsThroughRealObservabilityRegistry(t *testing.T) {
	var backendCalls, modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		if r.URL.Query().Get("service") != "inventory" {
			t.Error("route lost service")
		}
		data := map[string]any{"service": "inventory", "id": "1", "level": "ERROR", "operation": "shared", "message": "inventory error", "occurred_at": "2026-10-14 10:01:00"}
		if r.URL.Path == "/api/v1/agent/analyze_log_window" {
			data = map[string]any{"service": "inventory", "start": r.URL.Query().Get("start"), "end": r.URL.Query().Get("end"), "baseline_start": r.URL.Query().Get("baseline_start"), "baseline_end": r.URL.Query().Get("baseline_end"), "count_unit": "log_event", "totals": map[string]any{"events": 100000, "errors": 100}, "samples": []any{data}, "statistics_coverage": "complete_within_query", "samples_coverage": "partial_finite_window"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	content, _ := json.Marshal(map[string]any{"tools": map[string]any{"logs": map[string]any{"sources": map[string]any{"inventory_logs": map[string]any{"url": server.URL, "services": map[string]any{"inventory": "tool:logs.read"}}}}}})
	adapter, err := gcfg.NewAdapterContent(string(content))
	if err != nil {
		t.Fatal(err)
	}
	g.Cfg().SetAdapter(adapter)
	ctx := authz.WithPrincipal(t.Context(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	runner, err := BuildChatAgentWithOptions(ctx, ChatAgentOptions{ChatModel: &logRoutingModel{calls: &modelCalls}, MaxIterations: 5, DisableToolSearch: true, AllowedDomains: []tools.Domain{tools.DomainObservability}, AllowedToolNames: map[tools.Domain][]string{tools.DomainObservability: {"analyze_log_window", "read_log_evidence"}}})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := runner.Invoke(ctx, &UserMessage{Query: "Diagnose inventory logs in inventory_logs at 2026-10-14T10:00:00Z to 2026-10-14T10:10:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Content != "inventory diagnosis complete" || backendCalls.Load() != 2 || modelCalls.Load() != 5 {
		t.Fatalf("route answer=%s backend=%d model=%d", answer.Content, backendCalls.Load(), modelCalls.Load())
	}
}
