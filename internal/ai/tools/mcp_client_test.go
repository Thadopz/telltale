package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestLoadStreamableHTTPMCPToolsListsAndCallsTool(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("hello from streamable HTTP"), nil
	})
	httpServer := server.NewTestStreamableHTTPServer(mcpServer)
	defer httpServer.Close()

	loadedTools, err := loadStreamableHTTPMCPTools(
		context.Background(),
		httpServer.URL,
		nil,
		"test-client",
	)
	if err != nil {
		t.Fatalf("load streamable HTTP MCP tools: %v", err)
	}
	if len(loadedTools) != 1 {
		t.Fatalf("expected one MCP tool, got %d", len(loadedTools))
	}

	invokable, ok := loadedTools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("expected loaded MCP tool to be invokable")
	}
	output, err := invokable.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("call streamable HTTP MCP tool: %v", err)
	}
	if !strings.Contains(output, "hello from streamable HTTP") {
		t.Fatalf("unexpected MCP tool output: %s", output)
	}
}

func TestSREGymEndpointsUseStreamableHTTPPaths(t *testing.T) {
	t.Setenv("API_HOSTNAME", "sregym.local")
	t.Setenv("API_PORT", "8000")
	t.Setenv("MCP_SERVER_PORT", "9954")
	t.Setenv("SREGYM_KUBECTL_MCP_URL", "")
	t.Setenv("SREGYM_PROMETHEUS_MCP_URL", "")
	t.Setenv("SREGYM_JAEGER_MCP_URL", "")

	endpoints := sregymEndpoints(context.Background())
	expected := map[string]string{
		"kubectl":    "http://sregym.local:9954/kubectl/mcp",
		"prometheus": "http://sregym.local:9954/prometheus/mcp",
		"jaeger":     "http://sregym.local:9954/jaeger/mcp",
	}
	for _, endpoint := range endpoints {
		if expected[endpoint.Name] != endpoint.URL {
			t.Fatalf("unexpected %s endpoint: %s", endpoint.Name, endpoint.URL)
		}
	}
}
