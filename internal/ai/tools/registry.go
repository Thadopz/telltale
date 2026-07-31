package tools

import (
	"SREagent/internal/authz"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

type Scenario string

type Domain string

const (
	ScenarioChat  Scenario = "chat"
	ScenarioAIOps Scenario = "aiops"

	DomainGeneral       Domain = "general"
	DomainObservability Domain = "observability"
	DomainDatabase      Domain = "database"
	DomainPayment       Domain = "payment"
	DomainKnowledge     Domain = "knowledge"
)

// DomainToolSet keeps tools in stable business boundaries. Callers can expose
// each set through a dedicated agent without teaching the registry about agent
// orchestration.
type DomainToolSet struct {
	Domain Domain
	Tools  []tool.BaseTool
}

type Registry struct{}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) ToolsFor(ctx context.Context, scenario Scenario) ([]tool.BaseTool, error) {
	switch scenario {
	case ScenarioChat:
		return r.chatTools(ctx)
	case ScenarioAIOps:
		return r.aiOpsTools(ctx)
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
		toolList = append(toolList, set.Tools...)
	}
	return toolList, nil
}

// ChatDomainTools returns permission-filtered tools grouped by domain in a
// deterministic order. Empty groups are retained so configuration and tests
// can reason about the complete domain model.
func (r *Registry) ChatDomainTools(ctx context.Context) ([]DomainToolSet, error) {
	configuredTools, err := configuredMCPDomainTools(ctx)
	if err != nil {
		return nil, err
	}

	observabilityTools, err := r.optionalLogMcpTools(ctx)
	if err != nil {
		return nil, err
	}
	if err = appendSREGymLocalTools(ctx, &observabilityTools); err != nil {
		return nil, err
	}
	sregymTools, err := r.optionalSREGymMCPTools(ctx)
	if err != nil {
		return nil, err
	}
	observabilityTools = appendUniqueTools(ctx, observabilityTools, sregymTools)
	if err = appendTool(ctx, &observabilityTools, NewPrometheusAlertsQueryTool); err != nil {
		return nil, err
	}
	observabilityTools = appendUniqueTools(ctx, observabilityTools, configuredTools[DomainObservability])

	var databaseTools []tool.BaseTool
	if err = appendTool(ctx, &databaseTools, NewMysqlCrudTool); err != nil {
		return nil, err
	}
	if err = appendTool(ctx, &databaseTools, NewMysqlSlowSQLTool); err != nil {
		return nil, err
	}
	databaseTools = appendUniqueTools(ctx, databaseTools, configuredTools[DomainDatabase])

	var paymentTools []tool.BaseTool
	if err = appendPaymentAgentTools(ctx, &paymentTools); err != nil {
		return nil, err
	}
	paymentTools = appendUniqueTools(ctx, paymentTools, configuredTools[DomainPayment])

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
		{Domain: DomainPayment, Tools: paymentTools},
		{Domain: DomainKnowledge, Tools: knowledgeTools},
	}, nil
}

func (r *Registry) aiOpsTools(ctx context.Context) ([]tool.BaseTool, error) {
	configuredTools, err := configuredMCPDomainTools(ctx)
	if err != nil {
		return nil, err
	}

	toolList, err := r.optionalLogMcpTools(ctx)
	if err != nil {
		return nil, err
	}
	if err = appendSREGymLocalTools(ctx, &toolList); err != nil {
		return nil, err
	}
	sregymTools, err := r.optionalSREGymMCPTools(ctx)
	if err != nil {
		return nil, err
	}
	toolList = appendUniqueTools(ctx, toolList, sregymTools)
	if err = appendTool(ctx, &toolList, NewPrometheusAlertsQueryTool); err != nil {
		return nil, err
	}
	if err = appendTool(ctx, &toolList, NewMysqlSlowSQLTool); err != nil {
		return nil, err
	}
	if err = appendPaymentAgentTools(ctx, &toolList); err != nil {
		return nil, err
	}
	if err = appendTool(ctx, &toolList, NewQueryInternalDocsTool); err != nil {
		return nil, err
	}
	if err = appendTool(ctx, &toolList, NewGetCurrentTimeTool); err != nil {
		return nil, err
	}
	for _, domain := range []Domain{DomainObservability, DomainDatabase, DomainPayment, DomainKnowledge} {
		toolList = appendUniqueTools(ctx, toolList, configuredTools[domain])
	}
	return toolList, nil
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

func appendPaymentAgentTools(ctx context.Context, toolList *[]tool.BaseTool) error {
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

func (r *Registry) optionalLogMcpTools(ctx context.Context) ([]tool.BaseTool, error) {
	if !configBool(ctx, "tools.mcp_log.enabled", true) {
		g.Log().Info(ctx, "mcp log tools disabled by config")
		return []tool.BaseTool{}, nil
	}
	if !toolAllowedForRegistration(ctx, "mcp_log", authz.PermLogsRead) {
		g.Log().Info(ctx, "mcp log tools hidden by permission policy")
		return []tool.BaseTool{}, nil
	}

	toolList, err := cachedLogMcpTools(ctx)
	if err == nil {
		return wrapMCPLogTools(toolList), nil
	}
	if configBool(ctx, "tools.mcp_log.required", false) {
		return nil, err
	}
	g.Log().Warningf(ctx, "mcp log tools unavailable, continue without mcp log tools, err=%v", err)
	return []tool.BaseTool{}, nil
}

var mcpLogCache = struct {
	sync.Mutex
	url     string
	tools   []tool.BaseTool
	err     error
	expires time.Time
}{}

func cachedLogMcpTools(ctx context.Context) ([]tool.BaseTool, error) {
	url := getMCPLogURL(ctx)
	now := time.Now()

	mcpLogCache.Lock()
	if mcpLogCache.url == url && now.Before(mcpLogCache.expires) {
		tools := append([]tool.BaseTool(nil), mcpLogCache.tools...)
		err := mcpLogCache.err
		mcpLogCache.Unlock()
		return tools, err
	}
	mcpLogCache.Unlock()

	tools, err := GetLogMcpTool(ctx)
	ttl := time.Minute
	if err != nil {
		ttl = 30 * time.Second
	}

	mcpLogCache.Lock()
	mcpLogCache.url = url
	mcpLogCache.tools = append([]tool.BaseTool(nil), tools...)
	mcpLogCache.err = err
	mcpLogCache.expires = now.Add(ttl)
	mcpLogCache.Unlock()

	return append([]tool.BaseTool(nil), tools...), err
}

func wrapMCPLogTools(toolList []tool.BaseTool) []tool.BaseTool {
	wrapped := make([]tool.BaseTool, 0, len(toolList))
	for _, candidate := range toolList {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			continue
		}
		wrapped = append(wrapped, wrapAuthorizedToolWithPermission(invokable, authz.PermLogsRead))
	}
	return wrapped
}

func configBool(ctx context.Context, key string, fallback bool) bool {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return fallback
	}
	return v.Bool()
}
