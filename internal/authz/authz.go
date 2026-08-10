package authz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

type Permission string

const (
	PermDocsRead      Permission = "tool:docs.read"
	PermMetricsRead   Permission = "tool:metrics.read"
	PermLogsRead      Permission = "tool:logs.read"
	PermDatabaseRead  Permission = "tool:database.read"
	PermDatabaseWrite Permission = "tool:database.write"
	// PermMySQLRead and PermMySQLWrite are kept for old local configurations
	// and confirmed human-only mysql_crud calls.
	PermMySQLRead   Permission = "tool:mysql.read"
	PermMySQLWrite  Permission = "tool:mysql.write"
	PermPaymentRead Permission = "tool:payment.read"
	PermCodeRead    Permission = "tool:code.read"
	PermTimeRead    Permission = "tool:time.read"
	PermSREGym      Permission = "tool:sregym"
)

type AgentScope string

const (
	AgentScopeCoordinator   AgentScope = "coordinator"
	AgentScopePayment       AgentScope = "payment_agent"
	AgentScopeObservability AgentScope = "observability_agent"
	AgentScopeDatabase      AgentScope = "database_agent"
	AgentScopeKnowledge     AgentScope = "knowledge_agent"
)

type RiskLevel string

const (
	RiskReadOnly RiskLevel = "read_only"
	RiskWrite    RiskLevel = "write_or_risky"
)

type Principal struct {
	UserID   string   `json:"user_id"`
	Roles    []string `json:"roles"`
	TenantID string   `json:"tenant_id"`
}

type ToolCall struct {
	Name               string
	Permission         Permission
	Risk               RiskLevel
	RequiresConfirm    bool
	Confirmed          bool
	AgentScope         AgentScope
	AllowedAgentScopes []AgentScope
	RequiredCapability string
	ConversationID     string
	ArgumentsInJSON    string
	Operation          string
	OperationID        string
	ServerWriteEnabled bool
}

type AgentCapability struct {
	Scope        AgentScope
	TokenID      string
	ExpiresAt    time.Time
	Capabilities []string
}

type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type Authorizer struct{}

type contextKey struct{}
type confirmationContextKey struct{}
type agentScopeContextKey struct{}
type agentCapabilityContextKey struct{}
type quietLoggingContextKey struct{}

type ToolConfirmation struct {
	Token         string
	ToolName      string
	ArgumentsHash string
}

var deprecatedDatabasePermissionWarning sync.Once

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, normalizePrincipal(ctx, principal))
}

func WithAgentScope(ctx context.Context, scope AgentScope) context.Context {
	return context.WithValue(ctx, agentScopeContextKey{}, normalizeAgentScope(scope))
}

func AgentScopeFromContext(ctx context.Context) AgentScope {
	scope, ok := ctx.Value(agentScopeContextKey{}).(AgentScope)
	if !ok {
		return ""
	}
	return normalizeAgentScope(scope)
}

func NewAgentCapability(scope AgentScope, lifetime time.Duration) AgentCapability {
	return NewAgentCapabilityWithCapabilities(scope, lifetime)
}

func NewAgentCapabilityWithCapabilities(scope AgentScope, lifetime time.Duration, capabilities ...string) AgentCapability {
	if lifetime <= 0 {
		lifetime = 10 * time.Minute
	}
	return AgentCapability{
		Scope:        normalizeAgentScope(scope),
		TokenID:      randomTokenID(),
		ExpiresAt:    time.Now().Add(lifetime),
		Capabilities: normalizeCapabilities(capabilities),
	}
}

func WithAgentCapability(ctx context.Context, capability AgentCapability) context.Context {
	capability.Scope = normalizeAgentScope(capability.Scope)
	capability.Capabilities = normalizeCapabilities(capability.Capabilities)
	return context.WithValue(ctx, agentCapabilityContextKey{}, capability)
}

// WithQuietLogging suppresses diagnostic audit output for CLI commands whose
// stdout is a result stream. It does not change authorization decisions.
func WithQuietLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, quietLoggingContextKey{}, true)
}

func QuietLogging(ctx context.Context) bool {
	value, _ := ctx.Value(quietLoggingContextKey{}).(bool)
	return value
}

func AgentCapabilityFromContext(ctx context.Context) (AgentCapability, bool) {
	if ctx == nil {
		return AgentCapability{}, false
	}
	capability, ok := ctx.Value(agentCapabilityContextKey{}).(AgentCapability)
	if !ok || capability.TokenID == "" || capability.Scope == "" {
		return AgentCapability{}, false
	}
	return capability, true
}

func AgentCapabilitiesFromContext(ctx context.Context) []string {
	capability, ok := AgentCapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	return append([]string(nil), capability.Capabilities...)
}

func PrincipalFromContext(ctx context.Context) Principal {
	if principal, ok := ctx.Value(contextKey{}).(Principal); ok {
		return normalizePrincipal(ctx, principal)
	}
	return ConfiguredPrincipal(ctx)
}

// ConfiguredPrincipal is the only identity source used by the standalone
// runtime. There is no request-header or gateway-authentication identity.
func ConfiguredPrincipal(ctx context.Context) Principal {
	roles := []string{}
	if value, err := g.Cfg().Get(ctx, "identity.roles"); err == nil && value != nil {
		roles = value.Strings()
	}
	return normalizePrincipal(ctx, Principal{
		UserID:   configString(ctx, "identity.user_id", "local-user"),
		TenantID: configString(ctx, "identity.tenant_id", "local"),
		Roles:    roles,
	})
}

func WithToolConfirmation(ctx context.Context, confirmation ToolConfirmation) context.Context {
	confirmation.Token = strings.TrimSpace(confirmation.Token)
	confirmation.ToolName = strings.TrimSpace(confirmation.ToolName)
	confirmation.ArgumentsHash = strings.TrimSpace(confirmation.ArgumentsHash)
	return context.WithValue(ctx, confirmationContextKey{}, confirmation)
}

func IsToolCallConfirmed(ctx context.Context, toolName string, argumentsInJSON string) bool {
	confirmation, ok := ctx.Value(confirmationContextKey{}).(ToolConfirmation)
	if !ok {
		return false
	}
	return confirmation.Token != "" &&
		confirmation.ToolName == strings.TrimSpace(toolName) &&
		confirmation.ArgumentsHash == HashArguments(argumentsInJSON)
}

func HashArguments(argumentsInJSON string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(argumentsInJSON)))
	return hex.EncodeToString(sum[:])
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{}
}

func (a *Authorizer) Authorize(ctx context.Context, call ToolCall) Decision {
	if len(call.AllowedAgentScopes) > 0 && !agentScopeAllowed(call.AgentScope, call.AllowedAgentScopes) {
		return Decision{Allowed: false, Reason: "agent scope is not allowed"}
	}
	if call.RequiredCapability != "" && !agentCapabilityAllowed(ctx, call.AgentScope, call.RequiredCapability) {
		return Decision{Allowed: false, Reason: "agent capability is missing or expired"}
	}
	if !permissionsEnabled(ctx) {
		if !permissionsBypassAllowed(ctx) {
			return Decision{Allowed: false, Reason: "permissions cannot be disabled outside test or eval environment"}
		}
		return Decision{Allowed: true, Reason: "permissions disabled"}
	}
	principal := PrincipalFromContext(ctx)
	if call.Permission == "" {
		return Decision{Allowed: false, Reason: fmt.Sprintf("tool %s has no permission mapping", call.Name)}
	}
	if !principal.HasPermission(ctx, call.Permission) {
		return Decision{Allowed: false, Reason: fmt.Sprintf("missing permission %s", call.Permission)}
	}
	if call.Risk == RiskWrite {
		if !call.ServerWriteEnabled {
			return Decision{Allowed: false, Reason: "server write policy disabled"}
		}
		if strings.TrimSpace(call.OperationID) == "" {
			return Decision{Allowed: false, Reason: "write operation requires operation_id"}
		}
		if call.RequiresConfirm && !call.Confirmed {
			return Decision{Allowed: false, Reason: "write operation requires confirmation"}
		}
	}
	return Decision{Allowed: true, Reason: "allowed"}
}

func (p Principal) HasPermission(ctx context.Context, permission Permission) bool {
	permissions := PermissionsForRoles(ctx, p.Roles)
	for _, alias := range permissionAliases(permission) {
		if permissions[alias] {
			return true
		}
	}
	return false
}

func PermissionsForRoles(ctx context.Context, roles []string) map[Permission]bool {
	roles = normalizeRoles(ctx, roles)
	out := make(map[Permission]bool)
	for _, role := range roles {
		for _, permission := range scopesForRole(ctx, role) {
			for _, alias := range permissionAliases(permission) {
				out[alias] = true
			}
		}
	}
	return out
}

func permissionAliases(permission Permission) []Permission {
	switch permission {
	case PermDatabaseRead, PermMySQLRead:
		return []Permission{PermDatabaseRead, PermMySQLRead}
	case PermDatabaseWrite, PermMySQLWrite:
		return []Permission{PermDatabaseWrite, PermMySQLWrite}
	default:
		return []Permission{permission}
	}
}

func normalizePrincipal(ctx context.Context, principal Principal) Principal {
	principal.UserID = strings.TrimSpace(principal.UserID)
	principal.TenantID = strings.TrimSpace(principal.TenantID)
	principal.Roles = normalizeRoles(ctx, principal.Roles)
	if principal.TenantID == "" {
		principal.TenantID = "default"
	}
	return principal
}

func normalizeRoles(ctx context.Context, roles []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(roles)+1)
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		normalized = append(normalized, role)
	}
	if len(normalized) == 0 {
		role := strings.ToLower(strings.TrimSpace(configString(ctx, "permissions.default_role", "viewer")))
		if role == "" {
			role = "viewer"
		}
		normalized = append(normalized, role)
	}
	sort.Strings(normalized)
	return normalized
}

func scopesForRole(ctx context.Context, role string) []Permission {
	configKey := fmt.Sprintf("permissions.roles.%s.scopes", role)
	v, err := g.Cfg().Get(ctx, configKey)
	if err == nil && v != nil {
		rawScopes := v.Strings()
		if len(rawScopes) > 0 {
			return parsePermissions(ctx, rawScopes)
		}
	}

	switch role {
	case "admin":
		return []Permission{PermDocsRead, PermTimeRead, PermMetricsRead, PermLogsRead, PermPaymentRead, PermDatabaseRead, PermDatabaseWrite, PermSREGym}
	case "oncall":
		return []Permission{PermDocsRead, PermTimeRead, PermMetricsRead, PermLogsRead, PermPaymentRead, PermDatabaseRead}
	case "sregym":
		return []Permission{PermDocsRead, PermTimeRead, PermMetricsRead, PermLogsRead, PermSREGym}
	case "code_reader":
		return []Permission{PermCodeRead}
	default:
		return []Permission{PermDocsRead, PermTimeRead}
	}
}

func parsePermissions(ctx context.Context, scopes []string) []Permission {
	out := make([]Permission, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		permission := Permission(scope)
		if (permission == PermMySQLRead || permission == PermMySQLWrite) && !QuietLogging(ctx) {
			deprecatedDatabasePermissionWarning.Do(func() {
				g.Log().Warningf(ctx, "deprecated database permission %q; use tool:database.read or tool:database.write", permission)
			})
		}
		out = append(out, permission)
	}
	return out
}

func permissionsEnabled(ctx context.Context) bool {
	v, err := g.Cfg().Get(ctx, "permissions.enabled")
	if err != nil || v == nil {
		return true
	}
	return v.Bool()
}

func permissionsBypassAllowed(ctx context.Context) bool {
	environment := strings.ToLower(configString(ctx, "runtime.environment", "standalone"))
	return environment == "test" || environment == "eval"
}

func configString(ctx context.Context, key string, fallback string) string {
	v, err := g.Cfg().Get(ctx, key)
	if err != nil || v == nil {
		return fallback
	}
	if s := strings.TrimSpace(v.String()); s != "" {
		return s
	}
	return fallback
}

func normalizeAgentScope(scope AgentScope) AgentScope {
	return AgentScope(strings.ToLower(strings.TrimSpace(string(scope))))
}

func normalizeCapabilities(capabilities []string) []string {
	seen := make(map[string]struct{}, len(capabilities))
	result := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if capability == "" {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func agentScopeAllowed(scope AgentScope, allowed []AgentScope) bool {
	scope = normalizeAgentScope(scope)
	if scope == "" {
		return false
	}
	for _, candidate := range allowed {
		if scope == normalizeAgentScope(candidate) {
			return true
		}
	}
	return false
}

func agentCapabilityAllowed(ctx context.Context, scope AgentScope, requiredCapability string) bool {
	capability, ok := AgentCapabilityFromContext(ctx)
	if !ok || capability.Scope != normalizeAgentScope(scope) {
		return false
	}
	if !capability.ExpiresAt.IsZero() && !time.Now().Before(capability.ExpiresAt) {
		return false
	}
	return capabilityHas(capability.Capabilities, requiredCapability)
}

func capabilityHas(capabilities []string, required string) bool {
	required = strings.ToLower(strings.TrimSpace(required))
	if required == "" {
		return true
	}
	for _, capability := range capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), required) {
			return true
		}
	}
	return false
}

func randomTokenID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("cap-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
