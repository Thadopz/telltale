package tools

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/codeintel"
	"github.com/Thadopz/telltale/internal/authz"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

type Scenario string

type Domain string

const (
	ScenarioChat Scenario = "chat"

	DomainGeneral       Domain = "general"
	DomainObservability Domain = "observability"
	DomainDatabase      Domain = "database"
	DomainKnowledge     Domain = "knowledge"
)

// DomainToolSet keeps tools in stable functional boundaries. Callers can expose
// each set through a dedicated agent without teaching the registry about agent
// orchestration.
type DomainToolSet struct {
	Domain Domain
	Tools  []tool.BaseTool
}

type Registry struct {
	codeService     *codeintel.Service
	codeServiceErr  error
	codeServiceOnce sync.Once
	chatSets        []DomainToolSet
	chatSetsErr     error
	chatSetsOnce    sync.Once
}

func NewRegistry() *Registry {
	return &Registry{}
}

func NewRegistryWithCodeService(service *codeintel.Service) *Registry {
	return &Registry{codeService: service}
}

func (r *Registry) ToolsFor(ctx context.Context, scenario Scenario) ([]tool.BaseTool, error) {
	switch scenario {
	case ScenarioChat:
		return r.chatTools(ctx)
	default:
		return nil, fmt.Errorf("unsupported tool scenario: %s", scenario)
	}
}

func (r *Registry) chatTools(ctx context.Context) ([]tool.BaseTool, error) {
	sets, err := r.ChatDomainTools(ctx)
	if err != nil {
		return nil, err
	}

	var toolList []tool.BaseTool
	for _, set := range sets {
		for _, candidate := range set.Tools {
			info, infoErr := candidate.Info(ctx)
			if infoErr != nil {
				return nil, infoErr
			}
			metadata, known := LookupMetadata(info.Name)
			if known && !metadata.AgentVisible {
				continue
			}
			// Keep the legacy API from exposing capability-bound code tools.
			// Domain agents use ToolsForAgent, which applies the full scope check.
			if known && metadata.RequiredCapability != "" {
				continue
			}
			toolList = append(toolList, candidate)
		}
	}
	return toolList, nil
}

// ChatDomainTools returns permission-filtered tools grouped by domain in a
// deterministic order. Empty groups are retained so configuration and tests
// can reason about the complete domain model.
func (r *Registry) ChatDomainTools(ctx context.Context) ([]DomainToolSet, error) {
	r.chatSetsOnce.Do(func() {
		r.chatSets, r.chatSetsErr = r.buildChatDomainTools(ctx)
	})
	return r.chatSets, r.chatSetsErr
}

func (r *Registry) buildChatDomainTools(ctx context.Context) ([]DomainToolSet, error) {
	configuredTools, err := configuredMCPDomainTools(ctx)
	if err != nil {
		return nil, err
	}

	var observabilityTools []tool.BaseTool
	for _, factory := range []func() (tool.InvokableTool, error){
		NewAnalyzeLogWindowTool,
		NewReadLogEvidenceTool,
		NewDiscoverTelemetryTool,
		NewDiscoverMetricsTool,
		NewQueryMetricsTool,
		NewSearchLogsTool,
		NewSearchTracesTool,
		NewReadEvidenceTool,
	} {
		if err = appendTool(authz.WithAgentScope(ctx, authz.AgentScopeObservability), &observabilityTools, factory); err != nil {
			return nil, err
		}
	}
	if err = appendSREGymLocalTools(ctx, &observabilityTools); err != nil {
		return nil, err
	}
	if err = appendTool(ctx, &observabilityTools, NewPrometheusAlertsQueryTool); err != nil {
		return nil, err
	}
	observabilityTools = appendUniqueTools(ctx, observabilityTools, configuredTools[DomainObservability])
	if err = appendPaymentObservabilityTools(ctx, &observabilityTools); err != nil {
		return nil, err
	}
	if err = r.appendCodeTools(ctx, &observabilityTools); err != nil {
		return nil, err
	}

	var databaseTools []tool.BaseTool
	if err = appendTool(ctx, &databaseTools, NewMysqlCrudTool); err != nil {
		return nil, err
	}
	databaseRegistrationCtx := authz.WithAgentScope(ctx, authz.AgentScopeDatabase)
	for _, factory := range []func() (tool.InvokableTool, error){
		NewDBServerHealthTool,
		NewDBActiveQueriesTool,
		NewDBWaitEventsTool,
		NewDBLocksTool,
		NewDBSlowQueriesTool,
	} {
		if err = appendTool(databaseRegistrationCtx, &databaseTools, factory); err != nil {
			return nil, err
		}
	}
	databaseTools = appendUniqueTools(ctx, databaseTools, configuredTools[DomainDatabase])

	var generalTools []tool.BaseTool
	if err = appendTool(ctx, &generalTools, NewGetCurrentTimeTool); err != nil {
		return nil, err
	}

	var knowledgeTools []tool.BaseTool
	if err = appendTool(ctx, &knowledgeTools, NewQueryInternalDocsTool); err != nil {
		return nil, err
	}
	knowledgeTools = appendUniqueTools(ctx, knowledgeTools, configuredTools[DomainKnowledge])

	return []DomainToolSet{
		{Domain: DomainGeneral, Tools: generalTools},
		{Domain: DomainObservability, Tools: observabilityTools},
		{Domain: DomainDatabase, Tools: databaseTools},
		{Domain: DomainKnowledge, Tools: knowledgeTools},
	}, nil
}

func (r *Registry) ToolsForAgent(ctx context.Context, scenario Scenario, agentScope authz.AgentScope) ([]tool.BaseTool, error) {
	if scenario != ScenarioChat {
		return nil, fmt.Errorf("unsupported agent scope %q for scenario %q", agentScope, scenario)
	}

	sets, err := r.ChatDomainTools(ctx)
	if err != nil {
		return nil, err
	}
	targetDomain, ok := domainForAgentScope(agentScope)
	if !ok {
		return nil, fmt.Errorf("unsupported chat agent scope %q", agentScope)
	}
	for _, set := range sets {
		if set.Domain == targetDomain {
			return filterToolsForAgent(ctx, set.Tools, agentScope), nil
		}
	}
	return nil, nil
}

// FilterToolsForAgent applies the same visibility and scope rules to a caller
// supplied tool list. This keeps custom eval or embedding integrations from
// bypassing the production tool boundary used by Registry.ToolsForAgent.
func FilterToolsForAgent(ctx context.Context, candidates []tool.BaseTool, agentScope authz.AgentScope) []tool.BaseTool {
	return filterToolsForAgent(ctx, candidates, agentScope)
}

func filterToolsForAgent(ctx context.Context, candidates []tool.BaseTool, agentScope authz.AgentScope) []tool.BaseTool {
	filtered := make([]tool.BaseTool, 0, len(candidates))
	for _, candidate := range candidates {
		info, err := candidate.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		metadata, known := LookupMetadata(info.Name)
		if known && !metadata.AgentVisible {
			continue
		}
		if known && len(metadata.AllowedAgentScopes) > 0 && !containsAgentScope(metadata.AllowedAgentScopes, agentScope) {
			continue
		}
		if agentScope == authz.AgentScopeDatabase && !databaseAgentToolName(info.Name) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func containsAgentScope(scopes []authz.AgentScope, wanted authz.AgentScope) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func databaseAgentToolName(name string) bool {
	switch name {
	case "db_server_health", "db_active_queries", "db_wait_events", "db_locks", "db_slow_queries":
		return true
	default:
		return false
	}
}

func domainForAgentScope(scope authz.AgentScope) (Domain, bool) {
	switch scope {
	case authz.AgentScopeCoordinator:
		return DomainGeneral, true
	case authz.AgentScopeObservability:
		return DomainObservability, true
	case authz.AgentScopeDatabase:
		return DomainDatabase, true
	case authz.AgentScopeKnowledge:
		return DomainKnowledge, true
	default:
		return "", false
	}
}

func (r *Registry) appendCodeTools(ctx context.Context, toolList *[]tool.BaseTool) error {
	if !codeintel.Enabled(ctx) && r.codeService == nil {
		return nil
	}
	service, err := r.codeServiceFor(ctx)
	if err != nil {
		g.Log().Warningf(ctx, "code search source unavailable; hiding code tools: %v", err)
		return nil
	}
	if service == nil {
		return nil
	}

	registrationCtx := authz.WithAgentScope(ctx, authz.AgentScopeObservability)
	registrationCtx = authz.WithAgentCapability(registrationCtx, authz.NewAgentCapabilityWithCapabilities(authz.AgentScopeObservability, 10*time.Minute, "observability.diagnose", "code.read"))
	factories := []func() (tool.InvokableTool, error){
		func() (tool.InvokableTool, error) { return NewCodeSearchTool(service) },
		func() (tool.InvokableTool, error) { return NewCodeReadTool(service) },
	}
	for _, factory := range factories {
		if err := appendTool(registrationCtx, toolList, factory); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) codeServiceFor(ctx context.Context) (*codeintel.Service, error) {
	if r.codeService != nil {
		return r.codeService, nil
	}
	r.codeServiceOnce.Do(func() {
		r.codeService, r.codeServiceErr = codeintel.LoadFromConfig(ctx)
	})
	return r.codeService, r.codeServiceErr
}

func appendTool(ctx context.Context, toolList *[]tool.BaseTool, factory func() (tool.InvokableTool, error)) error {
	t, err := factory()
	if err != nil {
		return err
	}
	info, err := t.Info(ctx)
	if err != nil {
		return err
	}
	if !toolAllowedForRegistration(ctx, info.Name, "") {
		return nil
	}
	*toolList = append(*toolList, wrapAuthorizedTool(t))
	return nil
}

func appendPaymentObservabilityTools(ctx context.Context, toolList *[]tool.BaseTool) error {
	ctx = authz.WithAgentScope(ctx, authz.AgentScopeObservability)
	for _, factory := range []func() (tool.InvokableTool, error){
		NewQueryPaymentAnomalyTool,
		NewQueryPaymentMetricsTool,
		NewQueryPaymentOrderDetailTool,
		NewCheckPaymentConsistencyTool,
		NewSearchPaymentLogsTool,
		NewQueryPaymentStatusDistributionTool,
	} {
		if err := appendTool(ctx, toolList, factory); err != nil {
			return err
		}
	}
	return nil
}

func appendSREGymLocalTools(ctx context.Context, toolList *[]tool.BaseTool) error {
	if !sregymLocalToolsEnabled(ctx) {
		return nil
	}
	if !toolAllowedForRegistration(ctx, kubectlToolName, authz.PermSREGym) {
		g.Log().Info(ctx, "local sregym kubectl tool hidden by permission policy")
		return nil
	}
	return appendTool(ctx, toolList, NewSREGymKubectlTool)
}

func sregymLocalToolsEnabled(ctx context.Context) bool {
	if configBool(ctx, "tools.sregym.enabled", false) {
		return true
	}
	for _, role := range authz.PrincipalFromContext(ctx).Roles {
		if role == "sregym" {
			return true
		}
	}
	return false
}

func appendUniqueTools(ctx context.Context, base []tool.BaseTool, additions []tool.BaseTool) []tool.BaseTool {
	seen := map[string]struct{}{}
	for _, candidate := range base {
		if name := toolName(ctx, candidate); name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, candidate := range additions {
		name := toolName(ctx, candidate)
		if name != "" {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
		}
		base = append(base, candidate)
	}
	return base
}

func toolName(ctx context.Context, candidate tool.BaseTool) string {
	info, err := candidate.Info(ctx)
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}

func configBool(ctx context.Context, key string, fallback bool) bool {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return fallback
	}
	return v.Bool()
}
