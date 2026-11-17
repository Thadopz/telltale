package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/observability"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
)

type observedDomainAgentTool struct {
	inner  tool.InvokableTool
	name   string
	domain string
	scope  authz.AgentScope
}

func newObservedDomainAgentTool(agentTool tool.BaseTool, name string, domain string, scopes ...authz.AgentScope) (tool.BaseTool, error) {
	invokable, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("domain agent tool %s is not invokable", name)
	}
	scope := authz.AgentScope(name)
	if len(scopes) > 0 && scopes[0] != "" {
		scope = scopes[0]
	}
	return &observedDomainAgentTool{inner: invokable, name: name, domain: domain, scope: scope}, nil
}

func (t *observedDomainAgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *observedDomainAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (output string, err error) {
	if question := runstate.Question(ctx); question != "" {
		var request struct {
			Request string `json:"request"`
		}
		if err := json.Unmarshal([]byte(argumentsInJSON), &request); err != nil {
			return "", err
		}
		request.Request = "Original user task and constraints:\n" + question + "\nDelegated investigation:\n" + request.Request
		encoded, err := json.Marshal(request)
		if err != nil {
			return "", err
		}
		argumentsInJSON = string(encoded)
	}
	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "agent.domain.invoke",
		attribute.String("agent.name", t.name),
		attribute.String("agent.domain", t.domain),
		attribute.String("agent.kind", "domain"),
		attribute.String("agent.route.source", coordinatorDefinition.name),
	)
	ctx = authz.WithAgentScope(ctx, t.scope)
	ctx = authz.WithAgentCapability(ctx, authz.NewAgentCapabilityWithCapabilities(
		t.scope,
		10*time.Minute,
		agentCapabilities(t.scope, t.domain)...,
	))
	defer func() {
		elapsed := time.Since(startedAt)
		observability.RecordAgentRoute(ctx, t.name, t.domain, err)
		observability.RecordAgentRun(ctx, t.name, t.domain, "domain", false, elapsed, err)
		observability.EndSpan(span, err, attribute.Float64("agent.duration_ms", float64(elapsed)/float64(time.Millisecond)))
	}()

	return t.inner.InvokableRun(ctx, argumentsInJSON, options...)
}

func agentCapabilities(scope authz.AgentScope, domain string) []string {
	capabilities := []string{strings.TrimSpace(domain) + ".diagnose"}
	if scope == authz.AgentScopeObservability {
		capabilities = append(capabilities, "code.read")
	}
	return capabilities
}
