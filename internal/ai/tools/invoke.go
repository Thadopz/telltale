package tools

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/authz"

	"github.com/cloudwego/eino/components/tool"
)

func DescribeToolCall(ctx context.Context, toolName string, argumentsInJSON string) (authz.ToolCall, authz.Decision) {
	call := buildToolCall(ctx, toolName, argumentsInJSON, "")
	decision := authz.NewAuthorizer().Authorize(ctx, call)
	auditToolDecision(ctx, call, decision)
	return call, decision
}

func InvokeConfirmedTool(ctx context.Context, toolName string, argumentsInJSON string, token string) (string, error) {
	ctx = authz.WithToolConfirmation(ctx, authz.ToolConfirmation{
		Token:         token,
		ToolName:      toolName,
		ArgumentsHash: authz.HashArguments(argumentsInJSON),
	})
	t, err := toolByName(toolName)
	if err != nil {
		return "", err
	}
	return wrapAuthorizedTool(t).InvokableRun(ctx, argumentsInJSON)
}

func toolByName(toolName string) (tool.InvokableTool, error) {
	switch toolName {
	case "mysql_crud":
		return NewMysqlCrudTool()
	default:
		return nil, fmt.Errorf("unsupported executable tool %q", toolName)
	}
}
