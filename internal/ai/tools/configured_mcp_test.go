package tools

import (
	"context"
	"errors"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/container/gvar"
)

type configuredMCPTestTool struct {
	name string
}

func (t configuredMCPTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "configured MCP test tool"}, nil
}

func (t configuredMCPTestTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return `{"result":"ok"}`, nil
}

type configuredMCPStructuredTestTool struct{}

func (configuredMCPStructuredTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "structured_evidence", Desc: "configured MCP structured evidence test tool"}, nil
}

func (configuredMCPStructuredTestTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return `{"content":[{"type":"text","text":"{\"success\":true,\"status\":\"success\",\"evidence_id\":\"ev_nested_1\",\"can_use_as_evidence\":true,\"data_status\":\"present\",\"source_kind\":\"runtime_log\",\"runtime_verified\":true,\"observations\":[{\"kind\":\"log_error\",\"summary\":\"runtime error\"}],\"summary\":\"structured MCP evidence\"}"}]}`, nil
}

type configuredMCPFailingTool struct{}

func (configuredMCPFailingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "failing_remote_query", Desc: "configured MCP failure test tool"}, nil
}

func (configuredMCPFailingTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", errors.New("remote dependency unavailable")
}

func TestLoadConfiguredMCPServersGroupsAndNamespacesTools(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	configs := []MCPServerConfig{
		{
			Name:       "observability-platform",
			Domain:     DomainObservability,
			URL:        "http://observability.example/mcp",
			Permission: authz.PermMetricsRead,
			Enabled:    true,
			ReadOnly:   true,
		},
		{
			Name:       "payment-platform",
			Domain:     DomainPayment,
			URL:        "http://payment.example/mcp",
			Permission: authz.PermPaymentRead,
			Enabled:    true,
			ReadOnly:   true,
		},
	}

	toolsByDomain, err := loadConfiguredMCPServers(ctx, configs, func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error) {
		return []tool.BaseTool{configuredMCPTestTool{name: "find_incidents"}}, nil
	})
	if err != nil {
		t.Fatalf("load configured MCP servers: %v", err)
	}

	observabilityNames := toolNames(t, ctx, toolsByDomain[DomainObservability])
	if !observabilityNames["observability_platform__find_incidents"] {
		t.Fatalf("unexpected observability tools: %#v", observabilityNames)
	}
	paymentNames := toolNames(t, ctx, toolsByDomain[DomainPayment])
	if !paymentNames["payment_platform__find_incidents"] {
		t.Fatalf("unexpected payment tools: %#v", paymentNames)
	}

	invokable := toolsByDomain[DomainPayment][0].(tool.InvokableTool)
	paymentCtx := mcpAgentContext(ctx, authz.AgentScopePayment)
	output, err := invokable.InvokableRun(paymentCtx, `{"order_id":"P-1"}`)
	if err != nil {
		t.Fatalf("invoke configured MCP tool: %v", err)
	}
	parsed := evidence.Parse(output).Envelope
	if !parsed.CanUseAsEvidence || len(parsed.Observations) != 1 || parsed.Observations[0].Kind != evidence.ObservationMCPToolResult {
		t.Fatalf("expected MCP evidence envelope, got %s", output)
	}
}

func TestConfiguredMCPToolPreservesStructuredEvidenceEnvelope(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	config := MCPServerConfig{
		Name:       "structured-observer",
		Domain:     DomainObservability,
		URL:        "http://observability.example/mcp",
		Permission: authz.PermMetricsRead,
		Enabled:    true,
		ReadOnly:   true,
	}
	toolsByDomain, err := loadConfiguredMCPServers(ctx, []MCPServerConfig{config}, func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error) {
		return []tool.BaseTool{configuredMCPStructuredTestTool{}}, nil
	})
	if err != nil {
		t.Fatalf("load configured MCP server: %v", err)
	}
	invokable := toolsByDomain[DomainObservability][0].(tool.InvokableTool)
	observabilityCtx := mcpAgentContext(ctx, authz.AgentScopeObservability)
	output, err := invokable.InvokableRun(observabilityCtx, `{}`)
	if err != nil {
		t.Fatalf("invoke structured MCP tool: %v", err)
	}
	parsed := evidence.Parse(output).Envelope
	if parsed.EvidenceID == "ev_nested_1" || parsed.SourceKind != evidence.SourceRuntimeLog || parsed.RuntimeVerified {
		t.Fatalf("structured MCP metadata was not reissued safely: %s", output)
	}
	if parsed.TrustLevel != evidence.ContentTrustUntrustedExternal || parsed.VersionAlignment != evidence.VersionUnknown {
		t.Fatalf("structured MCP result must remain untrusted and unaligned: %s", output)
	}
	if len(parsed.Observations) != 2 || parsed.Observations[0].Kind != evidence.ObservationLogError {
		t.Fatalf("structured and generic observations were not merged: %s", output)
	}
}

func TestConfiguredMCPToolOpensCircuitAfterRepeatedFailures(t *testing.T) {
	serverName := "circuit-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	configuredMCPCircuits.Lock()
	delete(configuredMCPCircuits.entries, serverName)
	configuredMCPCircuits.Unlock()
	defer func() {
		configuredMCPCircuits.Lock()
		delete(configuredMCPCircuits.entries, serverName)
		configuredMCPCircuits.Unlock()
	}()

	wrapped := &configuredMCPTool{
		inner:          configuredMCPFailingTool{},
		info:           &schema.ToolInfo{Name: "circuit_query"},
		serverName:     serverName,
		timeout:        time.Second,
		maxOutputBytes: 1024,
		circuitKey:     serverName,
	}
	ctx := authz.WithAgentScope(context.Background(), authz.AgentScopeObservability)
	ctx = authz.WithAgentCapability(ctx, authz.NewAgentCapability(authz.AgentScopeObservability, time.Minute))

	for attempt := 0; attempt < configuredMCPCircuitThreshold; attempt++ {
		output, err := wrapped.InvokableRun(ctx, `{}`)
		if err != nil {
			t.Fatalf("unexpected MCP invocation error: %v", err)
		}
		if !strings.Contains(output, "remote dependency unavailable") {
			t.Fatalf("expected remote failure on attempt %d, got %s", attempt+1, output)
		}
	}
	output, err := wrapped.InvokableRun(ctx, `{}`)
	if err != nil {
		t.Fatalf("unexpected open-circuit error: %v", err)
	}
	if !strings.Contains(output, "circuit is open") {
		t.Fatalf("expected open-circuit response, got %s", output)
	}
}

func TestLoadConfiguredMCPServersSkipsServerWithoutPermission(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "viewer", Roles: []string{"viewer"}})
	config := MCPServerConfig{
		Name:       "observability",
		Domain:     DomainObservability,
		URL:        "http://observability.example/mcp",
		Permission: authz.PermMetricsRead,
		Enabled:    true,
		ReadOnly:   true,
	}
	loaderCalled := false

	toolsByDomain, err := loadConfiguredMCPServers(ctx, []MCPServerConfig{config}, func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error) {
		loaderCalled = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("load configured MCP servers: %v", err)
	}
	if loaderCalled {
		t.Fatal("expected permission check before MCP connection")
	}
	if len(toolsByDomain) != 0 {
		t.Fatalf("expected no registered tools, got %#v", toolsByDomain)
	}
}

func TestLoadConfiguredMCPServersHandlesOptionalAndRequiredFailures(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	optional := MCPServerConfig{
		Name:       "optional-observability",
		Domain:     DomainObservability,
		URL:        "http://optional.example/mcp",
		Permission: authz.PermMetricsRead,
		Enabled:    true,
		ReadOnly:   true,
	}
	loader := func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error) {
		return nil, errors.New("server unavailable")
	}

	toolsByDomain, err := loadConfiguredMCPServers(ctx, []MCPServerConfig{optional}, loader)
	if err != nil {
		t.Fatalf("expected optional failure to degrade, got %v", err)
	}
	if len(toolsByDomain) != 0 {
		t.Fatalf("expected no tools after optional failure, got %#v", toolsByDomain)
	}

	required := optional
	required.Name = "required-observability"
	required.Required = true
	_, err = loadConfiguredMCPServers(ctx, []MCPServerConfig{required}, loader)
	if err == nil || !strings.Contains(err.Error(), "required-observability") {
		t.Fatalf("expected required server error, got %v", err)
	}
}

func TestLoadConfiguredMCPServersRejectsUnsafeOrUnsupportedConfig(t *testing.T) {
	ctx := context.Background()
	base := MCPServerConfig{
		Name:       "example",
		Domain:     DomainObservability,
		URL:        "http://example.test/mcp",
		Permission: authz.PermMetricsRead,
		Enabled:    true,
	}
	loader := func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error) {
		t.Fatal("loader must not run for invalid config")
		return nil, nil
	}

	if _, err := loadConfiguredMCPServers(ctx, []MCPServerConfig{base}, loader); err == nil || !strings.Contains(err.Error(), "read_only=true") {
		t.Fatalf("expected read-only validation error, got %v", err)
	}

	base.ReadOnly = true
	base.Domain = DomainGeneral
	if _, err := loadConfiguredMCPServers(ctx, []MCPServerConfig{base}, loader); err == nil || !strings.Contains(err.Error(), "unsupported domain") {
		t.Fatalf("expected domain validation error, got %v", err)
	}
}

func TestMCPHeadersFromEnv(t *testing.T) {
	t.Setenv("PAYMENT_MCP_TOKEN", "Bearer test-token")
	headers, err := mcpHeadersFromEnv(map[string]string{"Authorization": "PAYMENT_MCP_TOKEN"})
	if err != nil {
		t.Fatalf("load headers from env: %v", err)
	}
	if headers["Authorization"] != "Bearer test-token" {
		t.Fatalf("unexpected headers: %#v", headers)
	}

	_, err = mcpHeadersFromEnv(map[string]string{"Authorization": "MISSING_MCP_TOKEN"})
	if err == nil || !strings.Contains(err.Error(), "MISSING_MCP_TOKEN") {
		t.Fatalf("expected missing environment variable error, got %v", err)
	}
}

func TestMCPServerConfigScansSnakeCaseFields(t *testing.T) {
	raw := gvar.New([]map[string]any{{
		"name":             "payment",
		"domain":           "payment",
		"url":              "http://payment.example/mcp",
		"permission":       "tool:payment.read",
		"enabled":          true,
		"required":         true,
		"read_only":        true,
		"headers_from_env": map[string]string{"Authorization": "PAYMENT_MCP_TOKEN"},
	}})

	var configs []MCPServerConfig
	if err := raw.Scan(&configs); err != nil {
		t.Fatalf("scan config: %v", err)
	}
	if len(configs) != 1 || !configs[0].ReadOnly || configs[0].HeadersFromEnv["Authorization"] != "PAYMENT_MCP_TOKEN" {
		t.Fatalf("unexpected scanned config: %#v", configs)
	}
}

func mcpAgentContext(ctx context.Context, scope authz.AgentScope) context.Context {
	ctx = authz.WithAgentScope(ctx, scope)
	return authz.WithAgentCapability(ctx, authz.NewAgentCapability(scope, time.Minute))
}
