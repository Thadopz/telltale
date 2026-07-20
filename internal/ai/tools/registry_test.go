package tools

import (
	"SREagent/internal/authz"
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestRegistryContinuesWhenMCPLogOptionalFails(t *testing.T) {
	ctx := context.Background()
	tools, err := NewRegistry().ToolsFor(ctx, ScenarioAIOps)
	if err != nil {
		t.Fatalf("expected optional mcp log failure to be ignored, got err=%v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected local tools to still be loaded")
	}
}

func TestRegistryIncludesReadOnlySlowSQLToolForAIOps(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	tools, err := NewRegistry().ToolsFor(ctx, ScenarioAIOps)
	if err != nil {
		t.Fatalf("load aiops tools: %v", err)
	}

	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatalf("load tool info: %v", err)
		}
		if info.Name == "mysql_slow_sql" {
			meta, ok := LookupMetadata(info.Name)
			if !ok {
				t.Fatal("expected mysql_slow_sql metadata")
			}
			if meta.Risk != RiskReadOnly || meta.RequiresConfirm {
				t.Fatalf("expected read-only slow sql metadata, got %#v", meta)
			}
			return
		}
	}

	t.Fatal("expected mysql_slow_sql in aiops tool registry")
}

func TestRegistryIncludesReadOnlyPaymentAgentToolsForAIOps(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	tools, err := NewRegistry().ToolsFor(ctx, ScenarioAIOps)
	if err != nil {
		t.Fatalf("load aiops tools: %v", err)
	}

	expected := map[string]bool{
		"query_payment_anomaly":             false,
		"query_payment_metrics":             false,
		"query_payment_order_detail":        false,
		"check_payment_consistency":         false,
		"search_payment_logs":               false,
		"query_payment_status_distribution": false,
	}
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatalf("load tool info: %v", err)
		}
		if _, ok := expected[info.Name]; !ok {
			continue
		}
		meta, ok := LookupMetadata(info.Name)
		if !ok {
			t.Fatalf("expected metadata for %s", info.Name)
		}
		if meta.Risk != RiskReadOnly || meta.RequiresConfirm {
			t.Fatalf("expected read-only metadata for %s, got %#v", info.Name, meta)
		}
		expected[info.Name] = true
	}
	for name, found := range expected {
		if !found {
			t.Fatalf("expected %s in aiops tool registry", name)
		}
	}
}

func TestRegistryFiltersToolsForViewer(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "viewer", Roles: []string{"viewer"}})
	tools, err := NewRegistry().ToolsFor(ctx, ScenarioChat)
	if err != nil {
		t.Fatalf("load chat tools: %v", err)
	}

	names := toolNames(t, ctx, tools)
	if !names["query_internal_docs"] || !names["get_current_time"] {
		t.Fatalf("expected viewer docs/time tools, got %#v", names)
	}
	for _, denied := range []string{"query_prometheus_alerts", "mysql_crud", "mysql_slow_sql", "query_payment_anomaly"} {
		if names[denied] {
			t.Fatalf("expected viewer not to see %s, got %#v", denied, names)
		}
	}
	if names[kubectlToolName] {
		t.Fatalf("expected viewer not to see %s, got %#v", kubectlToolName, names)
	}
}

func TestRegistryGroupsChatToolsByDomain(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	sets, err := NewRegistry().ChatDomainTools(ctx)
	if err != nil {
		t.Fatalf("load chat domain tools: %v", err)
	}

	byDomain := make(map[Domain]map[string]bool, len(sets))
	for _, set := range sets {
		byDomain[set.Domain] = toolNames(t, ctx, set.Tools)
	}

	assertToolInDomain(t, byDomain, DomainGeneral, "get_current_time")
	assertToolInDomain(t, byDomain, DomainObservability, "query_prometheus_alerts")
	assertToolInDomain(t, byDomain, DomainDatabase, "mysql_crud")
	assertToolInDomain(t, byDomain, DomainDatabase, "mysql_slow_sql")
	assertToolInDomain(t, byDomain, DomainPayment, "query_payment_anomaly")
	assertToolInDomain(t, byDomain, DomainKnowledge, "query_internal_docs")

	if byDomain[DomainObservability]["mysql_crud"] || byDomain[DomainDatabase]["query_payment_anomaly"] || byDomain[DomainPayment]["query_internal_docs"] {
		t.Fatalf("tools crossed domain boundaries: %#v", byDomain)
	}
}

func TestRegistryIncludesLocalKubectlToolForSREGymRole(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "sregym", Roles: []string{"sregym"}})
	tools, err := NewRegistry().ToolsFor(ctx, ScenarioChat)
	if err != nil {
		t.Fatalf("load chat tools: %v", err)
	}

	names := toolNames(t, ctx, tools)
	if !names[kubectlToolName] {
		t.Fatalf("expected %s for sregym role, got %#v", kubectlToolName, names)
	}
	meta, ok := LookupMetadata(kubectlToolName)
	if !ok {
		t.Fatalf("expected metadata for %s", kubectlToolName)
	}
	if meta.Permission != authz.PermSREGym {
		t.Fatalf("expected %s permission, got %#v", authz.PermSREGym, meta)
	}
}

func TestAuthorizedWrapperDeniesMysqlWriteWithoutServerPolicy(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "admin", Roles: []string{"admin"}})
	mysqlTool, err := NewMysqlCrudTool()
	if err != nil {
		t.Fatalf("create mysql tool: %v", err)
	}
	out, err := wrapAuthorizedTool(mysqlTool).InvokableRun(ctx, `{"operate_type":"update","sql":"update orders set status='done'"}`)
	if err != nil {
		t.Fatalf("invoke wrapped mysql tool: %v", err)
	}
	if !contains(out, "server write policy disabled") {
		t.Fatalf("expected write policy denial, got %s", out)
	}
}

func toolNames(t *testing.T, ctx context.Context, candidates []tool.BaseTool) map[string]bool {
	t.Helper()
	names := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatalf("load tool info: %v", err)
		}
		names[info.Name] = true
	}
	return names
}

func contains(s string, substr string) bool {
	return strings.Contains(s, substr)
}

func assertToolInDomain(t *testing.T, domains map[Domain]map[string]bool, domain Domain, name string) {
	t.Helper()
	if !domains[domain][name] {
		t.Fatalf("expected %s in %s domain, got %#v", name, domain, domains[domain])
	}
}
