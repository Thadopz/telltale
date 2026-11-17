package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/tool"
)

type scopedInvestigationTool struct {
	tool.InvokableTool
	scope  authz.AgentScope
	domain string
}

func (t *scopedInvestigationTool) InvokableRun(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
	// 跨调查角色复用工具时保留原权限域，调用者身份不变。
	ctx = authz.WithAgentScope(ctx, t.scope)
	ctx = authz.WithAgentCapability(ctx, authz.NewAgentCapabilityWithCapabilities(
		t.scope,
		10*time.Minute,
		agentCapabilities(t.scope, t.domain)...,
	))
	return t.InvokableTool.InvokableRun(ctx, arguments, options...)
}

func investigationTools(ctx context.Context, registry *tools.Registry, domain tools.Domain, allowedDomains []tools.Domain, allowedNames map[tools.Domain][]string) ([]tool.BaseTool, error) {
	if domain != tools.DomainObservability && domain != tools.DomainKnowledge {
		return nil, fmt.Errorf("unsupported investigation role %q", domain)
	}
	var result []tool.BaseTool
	for _, source := range []tools.Domain{tools.DomainObservability, tools.DomainDatabase, tools.DomainKnowledge} {
		if source == tools.DomainObservability && domain == tools.DomainObservability && !domainIsAllowed(source, allowedDomains) {
			continue
		}
		if source == tools.DomainDatabase && (domain != tools.DomainObservability || !domainIsAllowed(source, allowedDomains)) {
			continue
		}
		if source == tools.DomainKnowledge && domain != tools.DomainKnowledge {
			continue
		}
		sourceScope := authz.AgentScopeDatabase
		if source != tools.DomainDatabase {
			sourceScope = domainDefinitions[source].scope
		}
		candidates, err := registry.ToolsForAgent(ctx, tools.ScenarioChat, sourceScope)
		if err != nil {
			return nil, err
		}
		for _, candidate := range filterToolsByName(ctx, candidates, allowedNames[source]) {
			info, err := candidate.Info(ctx)
			if err != nil {
				return nil, err
			}
			code := info.Name == "code_search" || info.Name == "code_read" || info.Name == "code_snapshot"
			if source == tools.DomainObservability && ((domain == tools.DomainKnowledge) != code) {
				continue
			}
			if source != domain {
				invokable, ok := candidate.(tool.InvokableTool)
				if !ok {
					return nil, fmt.Errorf("investigation tool %s is not invokable", info.Name)
				}
				candidate = &scopedInvestigationTool{
					InvokableTool: invokable,
					scope:         sourceScope,
					domain:        string(source),
				}
			}
			result = append(result, candidate)
		}
	}
	return result, nil
}

func investigationDomainAllowed(domain tools.Domain, allowed []tools.Domain) bool {
	return domainIsAllowed(domain, allowed) || domain == tools.DomainObservability && domainIsAllowed(tools.DomainDatabase, allowed)
}
