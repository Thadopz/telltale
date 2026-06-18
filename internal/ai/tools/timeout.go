package tools

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

const defaultToolTimeout = 30 * time.Second

func toolCallTimeout(ctx context.Context) time.Duration {
	return configuredTimeout(ctx, "tools.call_timeout_seconds")
}

func mcpRegistrationTimeout(ctx context.Context) time.Duration {
	return configuredTimeout(ctx, "tools.mcp_registration_timeout_seconds")
}

func configuredTimeout(ctx context.Context, key string) time.Duration {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultToolTimeout
	}
	return time.Duration(value.Int()) * time.Second
}

type toolListResult struct {
	tools []tool.BaseTool
	err   error
}

func loadToolsWithTimeout(
	ctx context.Context,
	timeout time.Duration,
	load func(context.Context) ([]tool.BaseTool, error),
) ([]tool.BaseTool, error) {
	loadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan toolListResult, 1)
	go func() {
		tools, err := load(loadCtx)
		resultCh <- toolListResult{tools: tools, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.tools, result.err
	case <-loadCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, loadCtx.Err()
	}
}
