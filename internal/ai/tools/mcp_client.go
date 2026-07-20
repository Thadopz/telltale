package tools

import (
	"context"
	"fmt"

	e_mcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func loadStreamableHTTPMCPTools(
	ctx context.Context,
	endpoint string,
	headers map[string]string,
	clientName string,
) ([]tool.BaseTool, error) {
	options := make([]transport.StreamableHTTPCOption, 0, 1)
	if len(headers) > 0 {
		options = append(options, transport.WithHTTPHeaders(headers))
	}

	cli, err := client.NewStreamableHttpClient(endpoint, options...)
	if err != nil {
		return nil, fmt.Errorf("create streamable HTTP MCP client: %w", err)
	}

	if err = cli.Start(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("start streamable HTTP MCP client: %w", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    clientName,
		Version: "1.0.0",
	}
	if _, err = cli.Initialize(ctx, initRequest); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("initialize streamable HTTP MCP client: %w", err)
	}

	mcpTools, err := e_mcp.GetTools(ctx, &e_mcp.Config{Cli: cli})
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("list streamable HTTP MCP tools: %w", err)
	}
	return mcpTools, nil
}
