package tools

import (
	"SREagent/internal/ai/evidence"
	"SREagent/internal/authz"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const configuredMCPErrorCacheTTL = 30 * time.Second

type MCPServerConfig struct {
	Name           string            `json:"name"`
	Domain         Domain            `json:"domain"`
	URL            string            `json:"url"`
	Permission     authz.Permission  `json:"permission"`
	Enabled        bool              `json:"enabled"`
	Required       bool              `json:"required"`
	ReadOnly       bool              `json:"read_only"`
	HeadersFromEnv map[string]string `json:"headers_from_env"`
}

type mcpServerToolLoader func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error)

type configuredMCPTool struct {
	inner      tool.InvokableTool
	info       *schema.ToolInfo
	serverName string
	domain     Domain
}

type configuredMCPCacheEntry struct {
	tools      []tool.BaseTool
	err        error
	retryAfter time.Time
}

var configuredMCPCache = struct {
	sync.Mutex
	entries map[string]configuredMCPCacheEntry
}{
	entries: make(map[string]configuredMCPCacheEntry),
}

func (t *configuredMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *configuredMCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	output, err := t.inner.InvokableRun(ctx, argumentsInJSON, options...)
	if err != nil {
		return marshalToolError(err, "MCP tool "+t.info.Name+" failed"), nil
	}
	message := "MCP tool " + t.info.Name + " returned output"
	return marshalToolEvidence(output, message, []evidence.Observation{{
		Kind: evidence.ObservationMCPToolResult,
		Subject: evidence.SubjectRef{
			Type:    string(t.domain),
			Service: t.serverName,
			Fields:  map[string]string{"tool": t.info.Name},
		},
		Scope: evidence.EvidenceScope{Fields: map[string]string{
			"server":         t.serverName,
			"tool":           t.info.Name,
			"arguments_hash": authz.HashArguments(argumentsInJSON),
		}},
		Fields:  map[string]any{"server": t.serverName, "tool": t.info.Name, "domain": t.domain},
		Summary: message,
	}}), nil
}

func configuredMCPDomainTools(ctx context.Context) (map[Domain][]tool.BaseTool, error) {
	configs, err := configuredMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	return loadConfiguredMCPServers(ctx, configs, cachedConfiguredMCPTools)
}

func configuredMCPServers(ctx context.Context) ([]MCPServerConfig, error) {
	value, err := g.Cfg().Get(ctx, "tools.mcp_servers")
	if err != nil || value == nil || value.IsEmpty() {
		return nil, nil
	}

	var configs []MCPServerConfig
	if err = value.Scan(&configs); err != nil {
		return nil, fmt.Errorf("parse tools.mcp_servers: %w", err)
	}
	return configs, nil
}

func loadConfiguredMCPServers(
	ctx context.Context,
	configs []MCPServerConfig,
	loader mcpServerToolLoader,
) (map[Domain][]tool.BaseTool, error) {
	toolsByDomain := make(map[Domain][]tool.BaseTool)
	seenNames := make(map[string]bool)
	seenPrefixes := make(map[string]bool)

	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		config.Name = strings.TrimSpace(config.Name)
		config.Domain = Domain(strings.ToLower(strings.TrimSpace(string(config.Domain))))
		config.URL = strings.TrimSpace(config.URL)
		config.Permission = authz.Permission(strings.TrimSpace(string(config.Permission)))
		if err := validateMCPServerConfig(config, seenNames); err != nil {
			return nil, err
		}
		serverPrefix := normalizeMCPServerName(config.Name)
		if seenPrefixes[serverPrefix] {
			return nil, fmt.Errorf("mcp server %s has duplicate normalized prefix %q", config.Name, serverPrefix)
		}
		seenNames[config.Name] = true
		seenPrefixes[serverPrefix] = true

		if !toolAllowedForRegistration(ctx, "mcp_server:"+config.Name, config.Permission) {
			g.Log().Infof(ctx, "mcp server hidden by permission policy name=%s domain=%s", config.Name, config.Domain)
			continue
		}

		headers, err := mcpHeadersFromEnv(config.HeadersFromEnv)
		if err != nil {
			if config.Required {
				return nil, fmt.Errorf("load required mcp server %s: %w", config.Name, err)
			}
			g.Log().Warningf(ctx, "optional mcp server unavailable name=%s err=%v", config.Name, err)
			continue
		}

		discoveredTools, err := loadToolsWithTimeout(ctx, mcpRegistrationTimeout(ctx), func(loadCtx context.Context) ([]tool.BaseTool, error) {
			return loader(loadCtx, config, headers)
		})
		if err != nil {
			if config.Required {
				return nil, fmt.Errorf("load required mcp server %s: %w", config.Name, err)
			}
			g.Log().Warningf(ctx, "optional mcp server unavailable name=%s err=%v", config.Name, err)
			continue
		}

		wrappedTools, err := wrapConfiguredMCPTools(ctx, config, discoveredTools)
		if err != nil {
			if config.Required {
				return nil, fmt.Errorf("register required mcp server %s: %w", config.Name, err)
			}
			g.Log().Warningf(ctx, "optional mcp server tools rejected name=%s err=%v", config.Name, err)
			continue
		}

		toolsByDomain[config.Domain] = appendUniqueTools(ctx, toolsByDomain[config.Domain], wrappedTools)
		g.Log().Infof(ctx, "registered configured mcp server name=%s domain=%s tools=%d", config.Name, config.Domain, len(wrappedTools))
	}

	return toolsByDomain, nil
}

func validateMCPServerConfig(config MCPServerConfig, seenNames map[string]bool) error {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return fmt.Errorf("tools.mcp_servers entry has no name")
	}
	if seenNames[config.Name] {
		return fmt.Errorf("tools.mcp_servers contains duplicate name %q", config.Name)
	}
	if normalizeMCPServerName(config.Name) == "" {
		return fmt.Errorf("mcp server %s name must contain an ASCII letter or number", config.Name)
	}
	if strings.TrimSpace(config.URL) == "" {
		return fmt.Errorf("mcp server %s has no url", config.Name)
	}
	if strings.TrimSpace(string(config.Permission)) == "" {
		return fmt.Errorf("mcp server %s has no permission", config.Name)
	}
	if !config.ReadOnly {
		return fmt.Errorf("mcp server %s must declare read_only=true; write or destructive tools require an explicit Go policy wrapper", config.Name)
	}
	switch config.Domain {
	case DomainObservability, DomainDatabase, DomainPayment, DomainKnowledge:
		return nil
	default:
		return fmt.Errorf("mcp server %s has unsupported domain %q", config.Name, config.Domain)
	}
}

func mcpHeadersFromEnv(headerEnvNames map[string]string) (map[string]string, error) {
	if len(headerEnvNames) == 0 {
		return nil, nil
	}

	headers := make(map[string]string, len(headerEnvNames))
	for headerName, envName := range headerEnvNames {
		headerName = strings.TrimSpace(headerName)
		envName = strings.TrimSpace(envName)
		if headerName == "" || envName == "" {
			return nil, fmt.Errorf("headers_from_env contains an empty header or environment variable name")
		}
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return nil, fmt.Errorf("environment variable %s for header %s is empty", envName, headerName)
		}
		headers[headerName] = value
	}
	return headers, nil
}

func cachedConfiguredMCPTools(ctx context.Context, config MCPServerConfig, headers map[string]string) ([]tool.BaseTool, error) {
	cacheKey := configuredMCPServerCacheKey(config, headers)
	now := time.Now()

	configuredMCPCache.Lock()
	entry, found := configuredMCPCache.entries[cacheKey]
	if found && (entry.err == nil || now.Before(entry.retryAfter)) {
		cachedTools := append([]tool.BaseTool(nil), entry.tools...)
		configuredMCPCache.Unlock()
		return cachedTools, entry.err
	}
	configuredMCPCache.Unlock()

	discoveredTools, err := loadStreamableHTTPMCPTools(ctx, config.URL, headers, "sreagent-"+normalizeMCPServerName(config.Name))
	entry = configuredMCPCacheEntry{
		tools: append([]tool.BaseTool(nil), discoveredTools...),
		err:   err,
	}
	if err != nil {
		entry.retryAfter = now.Add(configuredMCPErrorCacheTTL)
	}

	configuredMCPCache.Lock()
	configuredMCPCache.entries[cacheKey] = entry
	configuredMCPCache.Unlock()
	return append([]tool.BaseTool(nil), discoveredTools...), err
}

func configuredMCPServerCacheKey(config MCPServerConfig, headers map[string]string) string {
	parts := []string{strings.TrimSpace(config.URL)}
	headerNames := make([]string, 0, len(headers))
	for name := range headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		parts = append(parts, name+"="+headers[name])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func wrapConfiguredMCPTools(ctx context.Context, config MCPServerConfig, discoveredTools []tool.BaseTool) ([]tool.BaseTool, error) {
	wrappedTools := make([]tool.BaseTool, 0, len(discoveredTools))
	serverPrefix := normalizeMCPServerName(config.Name)

	for _, candidate := range discoveredTools {
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			return nil, fmt.Errorf("tool from server %s is not invokable", config.Name)
		}
		info, err := invokable.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("read tool info from server %s: %w", config.Name, err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return nil, fmt.Errorf("server %s returned a tool without a name", config.Name)
		}

		namespacedInfo := *info
		namespacedInfo.Name = serverPrefix + "__" + info.Name
		wrapped := &configuredMCPTool{
			inner:      invokable,
			info:       &namespacedInfo,
			serverName: config.Name,
			domain:     config.Domain,
		}
		wrappedTools = append(wrappedTools, wrapAuthorizedToolWithPermission(wrapped, config.Permission))
	}
	return wrappedTools, nil
}

func normalizeMCPServerName(name string) string {
	var normalized strings.Builder
	lastUnderscore := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		isLetter := char >= 'a' && char <= 'z'
		isNumber := char >= '0' && char <= '9'
		if isLetter || isNumber {
			normalized.WriteRune(char)
			lastUnderscore = false
			continue
		}
		if normalized.Len() > 0 && !lastUnderscore {
			normalized.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(normalized.String(), "_")
}
