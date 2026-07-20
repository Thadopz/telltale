package tools

import (
	"context"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

/*
	GetLogMcpTool

https://cloud.tencent.com/developer/mcp/server/11710
https://cloud.tencent.com/document/product/614/118699#90415b66-8edb-43a9-ad5a-c2b0a97f5eaf

https://www.cloudwego.io/zh/docs/eino/ecosystem_integration/tool/tool_mcp/
https://mcp-go.dev/clients
*/
func GetLogMcpTool(ctx context.Context) ([]tool.BaseTool, error) {
	return loadToolsWithTimeout(ctx, mcpRegistrationTimeout(ctx), loadLogMcpTools)
}

func loadLogMcpTools(ctx context.Context) ([]tool.BaseTool, error) {
	return loadStreamableHTTPMCPTools(ctx, getMCPLogURL(ctx), nil, "sreagent-log-client")
}

func getMCPLogURL(ctx context.Context) string {
	for _, key := range []string{"tools.mcp_log.url", "mcp_url"} {
		v, err := g.Cfg().Get(ctx, key)
		if err != nil || v == nil {
			continue
		}
		url := strings.TrimSpace(v.String())
		if url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(os.Getenv("SUPERBIZ_MCP_LOG_URL")); url != "" {
		return url
	}
	return "https://YOUR_MCP_HOST/mcp"
}
