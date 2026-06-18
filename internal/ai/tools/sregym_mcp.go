package tools

import (
	"SREagent/internal/ai/evidence"
	"SREagent/internal/authz"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	e_mcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type sregymEndpoint struct {
	Name string
	URL  string
}

var sregymMCPCache = struct {
	sync.Mutex
	key     string
	tools   []tool.BaseTool
	err     error
	expires time.Time
}{}

type serializedSREGymTool struct {
	inner tool.InvokableTool
}

func (t *serializedSREGymTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *serializedSREGymTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	info, infoErr := t.inner.Info(ctx)
	toolName := "sregym"
	if infoErr == nil && info != nil && strings.TrimSpace(info.Name) != "" {
		toolName = info.Name
	}

	out, err := t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return marshalToolError(err, "SREGym tool "+toolName+" failed"), nil
	}
	out = truncateSREGymToolOutput(out, 2500)
	return marshalToolEvidence(out, "SREGym tool "+toolName+" returned output", []evidence.Observation{{
		Kind: evidence.ObservationMetricValue,
		Subject: evidence.SubjectRef{
			Type:    "kubernetes",
			Service: "sregym",
			Fields:  map[string]string{"tool": toolName},
		},
		Fields:  map[string]any{"tool": toolName},
		Summary: "SREGym tool " + toolName + " returned output",
	}}), nil
}

func (r *Registry) optionalSREGymMCPTools(ctx context.Context) ([]tool.BaseTool, error) {
	if !configBool(ctx, "tools.sregym.enabled", false) {
		return []tool.BaseTool{}, nil
	}
	if !toolAllowedForRegistration(ctx, "sregym", authz.PermSREGym) {
		g.Log().Info(ctx, "sregym tools hidden by permission policy")
		return []tool.BaseTool{}, nil
	}

	toolList, err := cachedSREGymMCPTools(ctx)
	if err != nil {
		g.Log().Warningf(ctx, "some sregym tools unavailable, continue with registered tools, err=%v", err)
	}
	return wrapSREGymMCPTools(toolList), nil
}

func cachedSREGymMCPTools(ctx context.Context) ([]tool.BaseTool, error) {
	key := sregymConfigKey(ctx)
	now := time.Now()

	sregymMCPCache.Lock()
	if sregymMCPCache.key == key && now.Before(sregymMCPCache.expires) {
		tools := append([]tool.BaseTool(nil), sregymMCPCache.tools...)
		err := sregymMCPCache.err
		sregymMCPCache.Unlock()
		return tools, err
	}
	sregymMCPCache.Unlock()

	tools, err := getSREGymMCPTools(ctx)
	ttl := time.Minute
	if err != nil {
		ttl = 30 * time.Second
	}

	sregymMCPCache.Lock()
	sregymMCPCache.key = key
	sregymMCPCache.tools = append([]tool.BaseTool(nil), tools...)
	sregymMCPCache.err = err
	sregymMCPCache.expires = now.Add(ttl)
	sregymMCPCache.Unlock()

	return append([]tool.BaseTool(nil), tools...), err
}

func getSREGymMCPTools(ctx context.Context) ([]tool.BaseTool, error) {
	headers := sregymHeaders(ctx)
	return loadSREGymEndpoints(ctx, sregymEndpoints(ctx), func(endpointCtx context.Context, endpoint sregymEndpoint) ([]tool.BaseTool, error) {
		return loadSREGymEndpoint(endpointCtx, endpoint, headers)
	})
}

type sregymEndpointLoader func(context.Context, sregymEndpoint) ([]tool.BaseTool, error)

func loadSREGymEndpoints(ctx context.Context, endpoints []sregymEndpoint, loader sregymEndpointLoader) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	var loadErrors []error
	for _, endpoint := range endpoints {
		if endpoint.URL == "" {
			continue
		}
		mcpTools, err := loadToolsWithTimeout(ctx, mcpRegistrationTimeout(ctx), func(endpointCtx context.Context) ([]tool.BaseTool, error) {
			return loader(endpointCtx, endpoint)
		})
		if err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("%s endpoint: %w", endpoint.Name, err))
			continue
		}
		g.Log().Infof(ctx, "registered sregym mcp endpoint=%s tools=%d", endpoint.Name, len(mcpTools))
		out = append(out, mcpTools...)
	}
	return out, errors.Join(loadErrors...)
}

func loadSREGymEndpoint(ctx context.Context, endpoint sregymEndpoint, headers map[string]string) ([]tool.BaseTool, error) {
	cli, err := client.NewSSEMCPClient(endpoint.URL, client.WithHeaders(headers))
	if err != nil {
		return nil, err
	}
	if err = cli.Start(ctx); err != nil {
		return nil, err
	}
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "sreagent-sregym-client",
		Version: "1.0.0",
	}
	if _, err = cli.Initialize(ctx, initRequest); err != nil {
		return nil, err
	}
	return e_mcp.GetTools(ctx, &e_mcp.Config{Cli: cli})
}

func sregymEndpoints(ctx context.Context) []sregymEndpoint {
	apiHost := envOrConfig(ctx, "API_HOSTNAME", "tools.sregym.api_hostname", "localhost")
	apiPort := envOrConfig(ctx, "API_PORT", "tools.sregym.api_port", "8000")
	mcpPort := envOrConfig(ctx, "MCP_SERVER_PORT", "tools.sregym.mcp_server_port", "9954")
	baseMCP := "http://" + apiHost + ":" + mcpPort
	baseAPI := "http://" + apiHost + ":" + apiPort
	endpoints := []sregymEndpoint{
		{Name: "kubectl", URL: envOrConfig(ctx, "SREGYM_KUBECTL_MCP_URL", "tools.sregym.kubectl_mcp_url", baseMCP+"/kubectl/sse")},
		{Name: "prometheus", URL: envOrConfig(ctx, "SREGYM_PROMETHEUS_MCP_URL", "tools.sregym.prometheus_mcp_url", baseMCP+"/prometheus/sse")},
		{Name: "jaeger", URL: envOrConfig(ctx, "SREGYM_JAEGER_MCP_URL", "tools.sregym.jaeger_mcp_url", baseMCP+"/jaeger/sse")},
	}
	if configBool(ctx, "tools.sregym.register_submit", false) {
		endpoints = append(endpoints, sregymEndpoint{
			Name: "submit",
			URL:  envOrConfig(ctx, "SREGYM_SUBMIT_MCP_URL", "tools.sregym.submit_mcp_url", baseAPI+"/submit_mcp/sse"),
		})
	}
	return endpoints
}

func sregymHeaders(ctx context.Context) map[string]string {
	sessionID := envOrConfig(ctx, "SREGYM_SESSION_ID", "tools.sregym.session_id", "")
	if sessionID == "" {
		sessionID = envOrConfig(ctx, "SUPERBIZ_SREGYM_SESSION_ID", "tools.sregym.session_id", "")
	}
	if sessionID == "" {
		return nil
	}
	return map[string]string{"sregym_ssid": sessionID}
}

func sregymConfigKey(ctx context.Context) string {
	parts := []string{}
	for _, endpoint := range sregymEndpoints(ctx) {
		parts = append(parts, endpoint.Name+"="+endpoint.URL)
	}
	for k, v := range sregymHeaders(ctx) {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "|")
}

func wrapSREGymMCPTools(toolList []tool.BaseTool) []tool.BaseTool {
	wrapped := make([]tool.BaseTool, 0, len(toolList))
	for _, candidate := range toolList {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			continue
		}
		wrapped = append(wrapped, wrapAuthorizedToolWithPermission(&serializedSREGymTool{inner: invokable}, authz.PermSREGym))
	}
	return wrapped
}

func truncateSREGymToolOutput(out string, maxRunes int) string {
	if maxRunes <= 0 {
		return out
	}
	runes := []rune(out)
	if len(runes) <= maxRunes {
		return out
	}
	return string(runes[:maxRunes]) + "\n...[truncated SREGym tool output]"
}

func envOrConfig(ctx context.Context, envKey string, configKey string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	v, err := g.Cfg().Get(ctx, configKey)
	if err == nil && v != nil {
		if s := strings.TrimSpace(v.String()); s != "" {
			return s
		}
	}
	return fallback
}
