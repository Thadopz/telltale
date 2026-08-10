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
