package models

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompleteRequestBudgetCoversCurrentToolAndSchema(t *testing.T) {
	inner := &providerCacheTestModel{}
	profile := contextbudget.ModelContextProfile{ContextWindowTokens: 10000, MaxOutputTokens: 1000, WorkingPromptCap: 9000}
	m := WithRequestBudget(inner, profile)
	messages := []*schema.Message{schema.UserMessage("diagnose"), {Role: schema.Tool, ToolCallID: "call", Content: strings.Repeat("中文", 5000)}}
	if _, err := m.Generate(t.Context(), messages); err == nil {
		t.Fatal("oversized current tool was sent")
	}
	if _, err := m.Stream(t.Context(), messages); err == nil {
		t.Fatal("oversized stream was sent")
	}
	if inner.generateCalls != 0 || inner.streamCalls != 0 {
		t.Fatal("guard ran after provider invocation")
	}
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "tool", Desc: strings.Repeat("schema", 6000)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Generate(t.Context(), []*schema.Message{schema.UserMessage("small")}); err == nil {
		t.Fatal("tool schemas omitted from budget")
	}
}

func logContinuationMessages() ([]*schema.Message, string) {
	env := evidence.NewDataWithObservations(map[string]any{
		"source_id": "inventory_logs", "capabilities": map[string]any{"read": true, "baseline": true}, "limitations": []any{"No trace backend; version/pod unavailable."}, "service": "inventory", "start": "2026-10-14T10:00:00+08:00", "end": "2026-10-14T10:10:00+08:00",
		"baseline_start": "2026-10-14T09:50:00+08:00", "baseline_end": "2026-10-14T10:00:00+08:00",
		"count_unit": "log_event", "totals": map[string]any{"events": 100000, "errors": 30000}, "baseline_totals": map[string]any{"events": 100000, "errors": 10000},
		"candidates":          []any{map[string]any{"operation": "callback", "current": map[string]any{"events": 100000, "errors": 30000}, "baseline": map[string]any{"events": 100000, "errors": 10000}, "event_error_share_delta": 0.2, "comparable": true}},
		"statistics_coverage": "complete_within_query", "samples_coverage": "partial_finite_window", "query_ref": "log_query",
		"samples": []any{map[string]any{"operation": "callback", "level": "INFO", "reference": "log_control", "message": strings.Repeat("raw control", 500)}},
	}, "Log comparison", []evidence.Observation{{Kind: evidence.ObservationMCPToolResult, Fields: map[string]any{"query_ref": "log_query", "sample_references": []any{"log_error", "log_control"}}}})
	return []*schema.Message{schema.UserMessage("diagnose"), schema.AssistantMessage("", []schema.ToolCall{{ID: "old", Type: "function", Function: schema.FunctionCall{Name: "analyze_log_window", Arguments: `{}`}}}), {Role: schema.Tool, ToolName: "analyze_log_window", ToolCallID: "old", Content: evidence.Marshal(env)}, schema.AssistantMessage("", []schema.ToolCall{{ID: "current", Type: "function", Function: schema.FunctionCall{Name: "read_log_evidence", Arguments: `{"reference":"log_error"}`}}}), {Role: schema.Tool, ToolName: "read_log_evidence", ToolCallID: "current", Content: "current excerpt"}}, env.Evidence.EvidenceID
}

func assertRetainedLogWindow(t *testing.T, content, evidenceID string) {
	t.Helper()
	var view struct {
		EvidenceID string `json:"evidence_id"`
		LogWindow  struct {
			Totals struct {
				Events int `json:"events"`
			} `json:"totals"`
			BaselineTotals struct {
				Errors int `json:"errors"`
			} `json:"baseline_totals"`
			Candidates []struct {
				Operation string  `json:"operation"`
				Delta     float64 `json:"event_error_share_delta"`
			} `json:"candidates"`
			Controls []struct {
				Reference string `json:"reference"`
			} `json:"controls"`
			Start string `json:"start"`
		} `json:"log_window"`
	}
	if err := json.Unmarshal([]byte(content), &view); err != nil {
		t.Fatal(err)
	}
	if view.EvidenceID != evidenceID || view.LogWindow.Totals.Events != 100000 || view.LogWindow.BaselineTotals.Errors != 10000 || len(view.LogWindow.Candidates) != 1 || view.LogWindow.Candidates[0].Operation != "callback" || view.LogWindow.Candidates[0].Delta != 0.2 || len(view.LogWindow.Controls) != 1 || view.LogWindow.Controls[0].Reference != "log_control" || view.LogWindow.Start == "" || !strings.Contains(content, "log_error") {
		t.Fatalf("lost window evidence: %s", content)
	}
	if strings.Contains(content, "raw control") || contextbudget.EstimateTextTokens(content) > 1600 {
		t.Fatal("compact view retained raw payload or exceeded budget")
	}
	if !strings.Contains(content, "inventory_logs") || !strings.Contains(content, "version/pod unavailable") || !strings.Contains(content, `"capabilities"`) {
		t.Fatal("source identity or capability gaps lost during compaction")
	}
}

func TestLogWindowCompactionIsIdempotentAndRetainsStatistics(t *testing.T) {
	messages, id := logContinuationMessages()
	first := consumedToolViews(messages)
	second := consumedToolViews(first)
	if second[2].Content != first[2].Content || second[4] != messages[4] {
		t.Fatal("repeated compaction changed compact or pending result")
	}
	assertRetainedLogWindow(t, second[2].Content, id)
	if messages[2].Extra["__telltale_consumed_tool_view"] != nil {
		t.Fatal("audit message was mutated")
	}
}

func TestConfiguredModelsReuseBudgetThroughProviderCache(t *testing.T) {
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"test","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	for _, cache := range []bool{false, true} {
		for _, factory := range []struct {
			name string
			new  func(context.Context) (model.ToolCallingChatModel, error)
		}{{"quick", OpenAIForDeepSeekV3Quick}, {"think", OpenAIForDeepSeekV31Think}, {"failover", OpenAIForFailover}} {
			t.Run(fmt.Sprintf("%s/cache=%t", factory.name, cache), func(t *testing.T) {
				role := map[string]any{"model": "test-model", "api_key": "test-key", "base_url": server.URL + "/v1", "context_window_tokens": 32768, "max_output_tokens": 1024}
				content, _ := json.Marshal(map[string]any{"quick_chat_model": role, "think_chat_model": role, "failover_chat_model": role, "provider_cache": map[string]any{"enabled": cache}})
				adapter, err := gcfg.NewAdapterContent(string(content))
				if err != nil {
					t.Fatal(err)
				}
				g.Cfg().SetAdapter(adapter)
				configured, err := factory.new(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				configured = WithRequestBudget(configured, contextbudget.ModelContextProfile{ContextWindowTokens: 16000, MaxOutputTokens: 2048})
				configured, err = configured.WithTools([]*schema.ToolInfo{{Name: "analyze_log_window"}, {Name: "read_log_evidence"}})
				if err != nil {
					t.Fatal(err)
				}
				depth := 0
				for current := configured; current != nil; {
					switch wrapped := current.(type) {
					case *requestBudgetModel:
						depth++
						if wrapped.profile.ContextWindowTokens != 16000 || wrapped.profile.MaxOutputTokens != 2048 {
							t.Fatal("lost tighter compatible budget")
						}
						current = wrapped.inner
					case *providerCacheModel:
						current = wrapped.inner
					default:
						current = nil
					}
				}
				if depth != 1 {
					t.Fatalf("budget wrapper count=%d", depth)
				}
				messages, id := logContinuationMessages()
				if _, err := configured.Generate(t.Context(), messages); err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.Unmarshal(<-requests, &wire); err != nil {
					t.Fatal(err)
				}
				assertRetainedLogWindow(t, wire.Messages[2].Content, id)
			})
		}
	}
}

func TestConsumedToolViewsPreservePairsAndAuditSource(t *testing.T) {
	env := evidence.NewDataWithObservations(map[string]any{"logs": strings.Repeat("stack", 10000)}, "Observed finite logs", []evidence.Observation{{Kind: evidence.ObservationLogError, Summary: "error"}})
	raw := evidence.Marshal(env)
	messages := []*schema.Message{schema.AssistantMessage("", []schema.ToolCall{{ID: "old"}}), {Role: schema.Tool, ToolCallID: "old", Content: raw}, schema.AssistantMessage("", []schema.ToolCall{{ID: "current"}}), {Role: schema.Tool, ToolCallID: "current", Content: "current pending result"}}
	views := consumedToolViews(messages)
	if len(views) != len(messages) || views[1].ToolCallID != "old" || views[3] != messages[3] {
		t.Fatal("tool group or pending result changed")
	}
	if strings.Contains(views[1].Content, "stackstack") || !strings.Contains(views[1].Content, env.Evidence.EvidenceID) || messages[1].Content != raw {
		t.Fatal("audit mutated or consumed result not reduced")
	}
}

func TestCompleteToolMessagesShareBudget(t *testing.T) {
	m := WithRequestBudget(&providerCacheTestModel{}, contextbudget.ModelContextProfile{ContextWindowTokens: 64000, MaxOutputTokens: 1000})
	var messages []*schema.Message
	for range 5 {
		messages = append(messages, &schema.Message{Role: schema.Tool, Content: strings.Repeat("中", 1800)})
	}
	if _, err := m.Generate(t.Context(), messages); err == nil {
		t.Fatal("concurrent results got separate 8000-token budgets")
	}
}
