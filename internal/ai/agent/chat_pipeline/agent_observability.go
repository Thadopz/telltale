package chat_pipeline

import (
	"SREagent/internal/observability"
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
)

type observedDomainAgentTool struct {
	inner  tool.InvokableTool
	name   string
	domain string
}

func newObservedDomainAgentTool(agentTool tool.BaseTool, name string, domain string) (tool.BaseTool, error) {
	invokable, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("domain agent tool %s is not invokable", name)
	}
	return &observedDomainAgentTool{inner: invokable, name: name, domain: domain}, nil
}

func (t *observedDomainAgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *observedDomainAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (output string, err error) {
	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "agent.domain.invoke",
		attribute.String("agent.name", t.name),
		attribute.String("agent.domain", t.domain),
		attribute.String("agent.kind", "domain"),
		attribute.String("agent.route.source", coordinatorDefinition.name),
	)
	defer func() {
		elapsed := time.Since(startedAt)
		observability.RecordAgentRoute(ctx, t.name, t.domain, err)
		observability.RecordAgentRun(ctx, t.name, t.domain, "domain", false, elapsed, err)
		observability.EndSpan(span, err, attribute.Float64("agent.duration_ms", float64(elapsed)/float64(time.Millisecond)))
	}()

	return t.inner.InvokableRun(ctx, argumentsInJSON, options...)
}
