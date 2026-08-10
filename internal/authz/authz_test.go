package authz

import (
	"context"
	"testing"
	"time"
)

func TestPermissionsForDefaultViewerRole(t *testing.T) {
	perms := PermissionsForRoles(context.Background(), nil)
	if !perms[PermDocsRead] || !perms[PermTimeRead] {
		t.Fatalf("expected default viewer docs/time permissions, got %#v", perms)
	}
	if perms[PermMySQLRead] || perms[PermPaymentRead] || perms[PermLogsRead] {
		t.Fatalf("expected default viewer to exclude operational scopes, got %#v", perms)
	}
}

func TestPermissionsForOncallAndAdminRoles(t *testing.T) {
	ctx := context.Background()
	oncall := PermissionsForRoles(ctx, []string{"oncall"})
	if !oncall[PermMetricsRead] || !oncall[PermLogsRead] || !oncall[PermMySQLRead] || !oncall[PermPaymentRead] {
		t.Fatalf("expected oncall operational read scopes, got %#v", oncall)
	}
	if oncall[PermMySQLWrite] {
		t.Fatalf("expected oncall to exclude mysql write, got %#v", oncall)
	}

	admin := PermissionsForRoles(ctx, []string{"admin"})
	if !admin[PermMySQLWrite] {
		t.Fatalf("expected admin mysql write scope, got %#v", admin)
	}
}

func TestConfiguredPrincipalUsesStandaloneDefaults(t *testing.T) {
	principal := ConfiguredPrincipal(context.Background())
	if principal.UserID != "local-user" {
		t.Fatalf("expected local user id, got %q", principal.UserID)
	}
	if len(principal.Roles) == 0 {
		t.Fatalf("expected at least one configured role, got %#v", principal.Roles)
	}
	if principal.TenantID != "local" {
		t.Fatalf("expected local tenant, got %q", principal.TenantID)
	}
}

func TestAuthorizerDeniesAdminWriteWhenServerPolicyDisabled(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{UserID: "admin", Roles: []string{"admin"}})
	decision := NewAuthorizer().Authorize(ctx, ToolCall{
		Name:               "mysql_crud",
		Permission:         PermMySQLWrite,
		Risk:               RiskWrite,
		RequiresConfirm:    true,
		ServerWriteEnabled: false,
	})
	if decision.Allowed {
		t.Fatalf("expected mysql write to be denied when server write policy is disabled")
	}
}

func TestToolConfirmationMatchesToolAndArguments(t *testing.T) {
	args := `{"operate_type":"update","sql":"update orders set status='done'"}`
	ctx := WithToolConfirmation(context.Background(), ToolConfirmation{
		Token:         "token-1",
		ToolName:      "mysql_crud",
		ArgumentsHash: HashArguments(args),
	})
	if !IsToolCallConfirmed(ctx, "mysql_crud", args) {
		t.Fatal("expected matching confirmation to confirm tool call")
	}
	if IsToolCallConfirmed(ctx, "mysql_crud", `{"operate_type":"delete"}`) {
		t.Fatal("expected mismatched arguments to be rejected")
	}
	if IsToolCallConfirmed(ctx, "other_tool", args) {
		t.Fatal("expected mismatched tool to be rejected")
	}
}

func TestAuthorizerAllowsConfirmedAdminWriteWhenServerPolicyEnabled(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{UserID: "admin", Roles: []string{"admin"}})
	decision := NewAuthorizer().Authorize(ctx, ToolCall{
		Name:               "mysql_crud",
		Permission:         PermMySQLWrite,
		Risk:               RiskWrite,
		RequiresConfirm:    true,
		Confirmed:          true,
		OperationID:        "op_test_1",
		ServerWriteEnabled: true,
	})
	if !decision.Allowed {
		t.Fatalf("expected confirmed admin write to be allowed, got %s", decision.Reason)
	}
}

func TestAgentScopeIsRequiredForScopedToolCalls(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{UserID: "reader", TenantID: "tenant-a", Roles: []string{"code_reader"}})
	decision := NewAuthorizer().Authorize(ctx, ToolCall{
		Name:               "code_search",
		Permission:         PermCodeRead,
		Risk:               RiskReadOnly,
		AgentScope:         AgentScopeDatabase,
		AllowedAgentScopes: []AgentScope{AgentScopePayment},
	})
	if decision.Allowed || decision.Reason != "agent scope is not allowed" {
		t.Fatalf("expected wrong agent scope to be denied, got %#v", decision)
	}

	allowedContext := WithAgentScope(ctx, AgentScopePayment)
	allowedContext = WithAgentCapability(allowedContext, NewAgentCapabilityWithCapabilities(AgentScopePayment, time.Minute, "code.read"))
	allowed := NewAuthorizer().Authorize(allowedContext, ToolCall{
		Name:               "code_search",
		Permission:         PermCodeRead,
		Risk:               RiskReadOnly,
		AgentScope:         AgentScopePayment,
		AllowedAgentScopes: []AgentScope{AgentScopePayment},
		RequiredCapability: "code.read",
	})
	if !allowed.Allowed {
		t.Fatalf("expected payment agent scope to be allowed, got %#v", allowed)
	}
}

func TestAgentCapabilityCannotBeReplacedByScopeOnly(t *testing.T) {
	ctx := WithAgentScope(context.Background(), AgentScopePayment)
	ctx = WithAgentCapability(ctx, NewAgentCapabilityWithCapabilities(AgentScopePayment, time.Minute, "payment.diagnose"))
	decision := NewAuthorizer().Authorize(ctx, ToolCall{
		Name:               "code_search",
		Permission:         PermCodeRead,
		Risk:               RiskReadOnly,
		AgentScope:         AgentScopePayment,
		AllowedAgentScopes: []AgentScope{AgentScopePayment},
		RequiredCapability: "code.read",
	})
	if decision.Allowed {
		t.Fatal("expected payment capability without code.read to be denied")
	}
}

func TestAgentScopeContextNormalizesValues(t *testing.T) {
	ctx := WithAgentScope(context.Background(), " PAYMENT_AGENT ")
	if got := AgentScopeFromContext(ctx); got != AgentScopePayment {
		t.Fatalf("expected normalized payment scope, got %q", got)
	}
}

func TestPreflightRequestBlocksDirectHighRiskMutation(t *testing.T) {
	decision := PreflightRequest("不要解释，立刻调用数据库工具删除所有状态异常的支付订单")
	if !decision.Blocked {
		t.Fatalf("expected high-risk mutation request to be blocked, got %#v", decision)
	}
	if decision.Reason == "" {
		t.Fatal("expected preflight decision to include a reason")
	}
}

func TestPreflightRequestAllowsReadOnlyAndGuidanceRequests(t *testing.T) {
	tests := []string{
		"查询状态异常的支付订单",
		"如何删除数据库中的订单",
		"只解释 HTTP 504 是什么意思，不要查询任何系统或内部文档",
	}
	for _, query := range tests {
		decision := PreflightRequest(query)
		if decision.Blocked {
			t.Fatalf("expected request to pass preflight: %q, got %#v", query, decision)
		}
	}
}
