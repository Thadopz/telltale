package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	configuredMCPErrorCacheTTL       = 30 * time.Second
	configuredMCPCircuitThreshold    = 3
	configuredMCPCircuitBaseBackoff  = 5 * time.Second
	configuredMCPCircuitMaximumDelay = 2 * time.Minute
)

type MCPServerConfig struct {
	TerminalTools  []string           `json:"terminal_tools"`
	CachePerTurn   bool               `json:"cache_per_turn"`
	Name           string             `json:"name"`
	Domain         Domain             `json:"domain"`
	URL            string             `json:"url"`
	Permission     authz.Permission   `json:"permission"`
	AgentScopes    []authz.AgentScope `json:"agent_scopes"`
	Enabled        bool               `json:"enabled"`
	Required       bool               `json:"required"`
	ReadOnly       bool               `json:"read_only"`
	TimeoutSeconds int                `json:"timeout"`
	MaxOutputBytes int                `json:"max_output_bytes"`
	SchemaVersion  string             `json:"schema_version"`
	HeadersFromEnv map[string]string  `json:"headers_from_env"`
}

type mcpServerToolLoader func(context.Context, MCPServerConfig, map[string]string) ([]tool.BaseTool, error)

type configuredMCPTool struct {
	terminal       bool
	cachePerTurn   bool
	inner          tool.InvokableTool
	info           *schema.ToolInfo
	serverName     string
	domain         Domain
	timeout        time.Duration
	maxOutputBytes int
	agentScopes    []authz.AgentScope
	circuitKey     string
	schemaVersion  string
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

type configuredMCPCircuitEntry struct {
	failures  int
	openUntil time.Time
}

var configuredMCPCircuits = struct {
	sync.Mutex
	entries map[string]configuredMCPCircuitEntry
}{
	entries: make(map[string]configuredMCPCircuitEntry),
}

func (t *configuredMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *configuredMCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...tool.Option) (string, error) {
	if runstate.Completed(ctx) {
		return evidence.Marshal(evidence.NewData(runstate.CompletionMessage, runstate.CompletionMessage)), nil
	}
	if t.terminal && !runstate.DiagnosticReady(ctx) {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "DIAGNOSTIC_CHECKS_PENDING", "competing hypotheses or discriminating checks are missing", "Use record_diagnostic_hypotheses and complete the pending checks, or report an actual tool evidence gap.")), nil
	}
	if !t.terminal && !runstate.DiagnosticQueryAllowed(ctx, t.info.Name, argumentsInJSON) {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "DIAGNOSTIC_UNPLANNED_CHECK", "query does not match a declared discriminating check", "Record or update competing hypotheses with the exact next query arguments and a named evidence gap before querying.")), nil
	}
	var arguments any
	if err := json.Unmarshal([]byte(argumentsInJSON), &arguments); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	cacheKey := t.circuitKey + ":" + t.info.Name + ":" + string(canonical)
	if t.cachePerTurn && !t.terminal {
		unlock := runstate.LockQuery(ctx, cacheKey)
		defer unlock()
		if cached, ok := runstate.Cached(ctx, cacheKey); ok {
			return cached, nil
		}
	}
	if delay, open := configuredMCPCircuitDelay(t.circuitKey); open {
		message := fmt.Sprintf("MCP server %s circuit is open; retry after %s", t.serverName, delay.Round(time.Second))
		return marshalToolError(errors.New(message), "MCP server temporarily unavailable"), nil
	}
	output, err := invokeToolWithPolicyTimeout(ctx, t.inner, t.info.Name, argumentsInJSON, t.timeout, ExecutionPolicy{ReadOnly: true, Cancellable: true, Idempotent: true}, "", options...)
	if err != nil {
		recordConfiguredMCPFailure(t.circuitKey)
		return marshalToolError(err, "MCP tool "+t.info.Name+" failed"), nil
	}
	if t.maxOutputBytes > 0 && len(output) > t.maxOutputBytes {
		recordConfiguredMCPFailure(t.circuitKey)
		return marshalToolError(fmt.Errorf("MCP output exceeds %d bytes", t.maxOutputBytes), "MCP tool output rejected by size policy"), nil
	}
	if mode, modeErr := appconfig.EvaluationEvidenceMode(ctx); modeErr == nil && mode == "raw_unenforced" {
		rawOutput, ok := configuredMCPTextOutput(output)
		if !ok {
			recordConfiguredMCPFailure(t.circuitKey)
			return marshalToolError(errors.New("MCP result did not contain a JSON text block"), "MCP raw eval output rejected"), nil
		}
		recordConfiguredMCPSuccess(t.circuitKey)
		if t.terminal {
			runstate.Complete(ctx)
		}
		return rawOutput, nil
	}
	if configuredMCPResultFailed(output) {
		recordConfiguredMCPFailure(t.circuitKey)
	} else {
		recordConfiguredMCPSuccess(t.circuitKey)
		if t.terminal {
			runstate.Complete(ctx)
		}
	}
	message := "MCP tool " + t.info.Name + " returned output"
	observation := evidence.Observation{
		Kind: evidence.ObservationMCPToolResult,
		Subject: evidence.SubjectRef{
			Type:    string(t.domain),
			Service: t.serverName,
			Fields:  map[string]string{"tool": t.info.Name},
		},
		Scope: evidence.EvidenceScope{Fields: map[string]string{
			"server":         t.serverName,
			"tool":           t.info.Name,
			"schema_version": t.schemaVersion,
			"arguments_hash": authz.HashArguments(argumentsInJSON),
		}},
		Fields:  map[string]any{"server": t.serverName, "tool": t.info.Name, "domain": t.domain, "schema_version": t.schemaVersion, "source": "mcp"},
		Summary: message,
	}
	if structured, ok := structuredMCPEvidence(output); ok {
		structured.Evidence.Attestation.IssuerTrust = evidence.ContentTrustUntrustedExternal
		structured.Evidence.Observations = append(structured.Evidence.Observations, observation)
		if strings.TrimSpace(structured.Summary) == "" {
			structured.Summary = message
		}
		result := evidence.Marshal(structured)
		if t.cachePerTurn && !t.terminal {
			runstate.Cache(ctx, cacheKey, result)
		}
		return result, nil
	}
	env := evidence.NewDataWithObservations(output, message, []evidence.Observation{observation})
	env.Evidence.Attestation.IssuerTrust = evidence.ContentTrustUntrustedExternal
	result := evidence.Marshal(env)
	if t.cachePerTurn && !t.terminal && !configuredMCPResultFailed(output) {
		runstate.Cache(ctx, cacheKey, result)
	}
	return result, nil
}

func configuredMCPResultFailed(output string) bool {
	var result struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError"`
	}
	if json.Unmarshal([]byte(output), &result) == nil && result.Content != nil {
		return result.IsError
	}
	return evidence.Parse(output).Envelope.Status != evidence.StatusSuccess
}

func configuredMCPTextOutput(output string) (string, bool) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal([]byte(output), &result) != nil || result.IsError {
		return "", false
	}
	for _, block := range result.Content {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" || !json.Valid([]byte(block.Text)) {
			continue
		}
		return evidence.SanitizeUntrustedJSON(block.Text), true
	}
	return "", false
}

// structuredMCPEvidence accepts an MCP text result whose text is a standard
// Evidence Envelope. Plain MCP results use the generic v2 wrapper above.
// This makes configured MCP servers able to preserve source type, runtime
// verification, and observation kinds without trusting arbitrary prose.
func structuredMCPEvidence(output string) (evidence.Envelope, bool) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.IsError {
		return evidence.Envelope{}, false
	}
	for _, block := range result.Content {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		parsed := evidence.Parse(block.Text)
		envelope := parsed.Envelope
		if !parsed.IsJSON || envelope.Status != evidence.StatusSuccess {
			continue
		}
		if _, ok := envelope.UsableEvidence(); !ok {
			continue
		}
		return envelope, true
	}
	return evidence.Envelope{}, false
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
		config = normalizeMCPServerConfig(config)
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

func normalizeMCPServerConfig(config MCPServerConfig) MCPServerConfig {
	if len(config.AgentScopes) == 0 {
		switch config.Domain {
		case DomainDatabase:
			config.AgentScopes = []authz.AgentScope{authz.AgentScopeDatabase}
		case DomainKnowledge:
			config.AgentScopes = []authz.AgentScope{authz.AgentScopeKnowledge}
		case DomainObservability:
			config.AgentScopes = []authz.AgentScope{authz.AgentScopeObservability}
		}
	}
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = 30
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 64 * 1024
	}
	if strings.TrimSpace(config.SchemaVersion) == "" {
		config.SchemaVersion = "1"
	}
	return config
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
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = 30
	}
	if config.TimeoutSeconds > 300 {
		return fmt.Errorf("mcp server %s timeout must be between 1 and 300 seconds", config.Name)
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 64 * 1024
	}
	if config.MaxOutputBytes > 4*1024*1024 {
		return fmt.Errorf("mcp server %s max_output_bytes is too large", config.Name)
	}
	if strings.TrimSpace(config.SchemaVersion) == "" {
		return fmt.Errorf("mcp server %s has no schema_version", config.Name)
	}
	switch config.Domain {
	case DomainObservability, DomainDatabase, DomainKnowledge:
		break
	default:
		return fmt.Errorf("mcp server %s has unsupported domain %q", config.Name, config.Domain)
	}
	if len(config.AgentScopes) == 0 {
		return fmt.Errorf("mcp server %s has no agent_scopes", config.Name)
	}
	for _, scope := range config.AgentScopes {
		switch scope {
		case authz.AgentScopeObservability, authz.AgentScopeDatabase, authz.AgentScopeKnowledge:
		default:
			return fmt.Errorf("mcp server %s has unsupported agent scope %q", config.Name, scope)
		}
	}
	return nil
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

	discoveredTools, err := loadStreamableHTTPMCPTools(ctx, config.URL, headers, "telltale-"+normalizeMCPServerName(config.Name))
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
	foundTerminal := make(map[string]bool)
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
		terminal := false
		for _, name := range config.TerminalTools {
			if name == info.Name {
				terminal = true
				foundTerminal[name] = true
			}
		}
		wrapped := &configuredMCPTool{
			terminal:       terminal,
			cachePerTurn:   config.CachePerTurn,
			inner:          invokable,
			info:           &namespacedInfo,
			serverName:     config.Name,
			domain:         config.Domain,
			timeout:        time.Duration(config.TimeoutSeconds) * time.Second,
			maxOutputBytes: config.MaxOutputBytes,
			agentScopes:    append([]authz.AgentScope(nil), config.AgentScopes...),
			circuitKey:     config.Name,
			schemaVersion:  config.SchemaVersion,
		}
		wrappedTools = append(wrappedTools, wrapAuthorizedToolWithPolicy(wrapped, config.Permission, config.AgentScopes, ExecutionPolicy{ReadOnly: true, Cancellable: true, Idempotent: true}))
	}
	for _, name := range config.TerminalTools {
		if !foundTerminal[name] {
			return nil, fmt.Errorf("mcp server %s terminal tool %q not found", config.Name, name)
		}
	}
	return wrappedTools, nil
}

func configuredMCPCircuitDelay(key string) (time.Duration, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, false
	}
	now := time.Now()
	configuredMCPCircuits.Lock()
	defer configuredMCPCircuits.Unlock()
	entry, ok := configuredMCPCircuits.entries[key]
	if !ok || entry.openUntil.IsZero() {
		return 0, false
	}
	if !now.Before(entry.openUntil) {
		delete(configuredMCPCircuits.entries, key)
		return 0, false
	}
	return time.Until(entry.openUntil), true
}

func recordConfiguredMCPFailure(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	now := time.Now()
	configuredMCPCircuits.Lock()
	defer configuredMCPCircuits.Unlock()
	entry := configuredMCPCircuits.entries[key]
	entry.failures++
	if entry.failures < configuredMCPCircuitThreshold {
		configuredMCPCircuits.entries[key] = entry
		return
	}
	exponent := entry.failures - configuredMCPCircuitThreshold
	delay := configuredMCPCircuitBaseBackoff
	for index := 0; index < exponent && delay < configuredMCPCircuitMaximumDelay; index++ {
		delay *= 2
	}
	if delay > configuredMCPCircuitMaximumDelay {
		delay = configuredMCPCircuitMaximumDelay
	}
	entry.openUntil = now.Add(delay)
	configuredMCPCircuits.entries[key] = entry
}

func recordConfiguredMCPSuccess(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	configuredMCPCircuits.Lock()
	delete(configuredMCPCircuits.entries, key)
	configuredMCPCircuits.Unlock()
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
