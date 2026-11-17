package tools

import (
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

func TestConfiguredMCPLiveRepeatedSuccess(t *testing.T) {
	endpoint := os.Getenv("TELLTALE_RCAEVAL_MCP_URL")
	if endpoint == "" {
		t.Skip("set TELLTALE_RCAEVAL_MCP_URL to a real RCAEval case server")
	}
	ctx := t.Context()
	remote, err := loadStreamableHTTPMCPTools(ctx, endpoint, nil, "telltale-live-regression")
	if err != nil {
		t.Fatal(err)
	}
	var describe tool.InvokableTool
	for _, candidate := range remote {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == "describe_case" {
			describe = candidate.(tool.InvokableTool)
			break
		}
	}
	if describe == nil {
		t.Fatal("real server has no describe_case tool")
	}
	wrapped := &configuredMCPTool{
		inner:      describe,
		serverName: "rcaeval-live-test",
		circuitKey: "rcaeval-live-test",
		timeout:    10 * time.Second,
	}
	wrapped.info, err = describe.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		recordConfiguredMCPSuccess(wrapped.circuitKey)
	})
	for range 5 {
		if _, err := wrapped.InvokableRun(ctx, "{}"); err != nil {
			t.Fatal(err)
		}
		if _, open := configuredMCPCircuitDelay(wrapped.circuitKey); open {
			t.Fatal("successful real MCP calls opened the circuit")
		}
	}
	raw, err := describe.InvokableRun(ctx, `{"unexpected":true}`)
	if err == nil && !configuredMCPResultFailed(raw) {
		t.Fatal("real MCP tool error was counted as a success")
	}
	cachedCtx := runstate.WithState(ctx)
	wrapped.cachePerTurn = true
	first, err := wrapped.InvokableRun(cachedCtx, "{}")
	if err != nil {
		t.Fatal(err)
	}
	second, err := wrapped.InvokableRun(cachedCtx, "{ }")
	if err != nil || second != first {
		t.Fatal("equivalent real queries did not reuse the original result")
	}
	for _, candidate := range remote {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "submit" {
			continue
		}
		terminal := &configuredMCPTool{
			inner:      candidate.(tool.InvokableTool),
			info:       info,
			terminal:   true,
			timeout:    10 * time.Second,
			circuitKey: "rcaeval-live-terminal",
		}
		terminalCtx := runstate.WithState(ctx)
		if _, err := terminal.InvokableRun(terminalCtx, `{"ranks":["__not_a_candidate__"]}`); err != nil {
			t.Fatal(err)
		}
		if runstate.Completed(terminalCtx) {
			t.Fatal("rejected submission ended the run")
		}
		raw, err := describe.InvokableRun(ctx, "{}")
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		var overview struct {
			Services []string `json:"candidate_services"`
		}
		if err := json.Unmarshal([]byte(response.Content[0].Text), &overview); err != nil {
			t.Fatal(err)
		}
		arguments, err := json.Marshal(map[string]any{"ranks": overview.Services[:1]})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := terminal.InvokableRun(terminalCtx, string(arguments)); err != nil {
			t.Fatal(err)
		}
		if !runstate.Completed(terminalCtx) {
			t.Fatal("accepted real submission did not end the run")
		}
		return
	}
	t.Fatal("real server has no submit tool")
}
