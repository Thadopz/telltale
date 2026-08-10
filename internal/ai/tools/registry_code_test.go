package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/codeintel"
	"github.com/Thadopz/telltale/internal/authz"
)

func TestRegistryOnlyExposesCodeToolsToPaymentAgent(t *testing.T) {
	service := testCodeService(t)
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{
		UserID:   "reader",
		TenantID: "tenant-a",
		Roles:    []string{"code_reader"},
	})
	registry := NewRegistryWithCodeService(service)

	paymentTools, err := registry.ToolsForAgent(ctx, ScenarioChat, authz.AgentScopePayment)
	if err != nil {
		t.Fatalf("load payment agent tools: %v", err)
	}
	paymentNames := toolNames(t, ctx, paymentTools)
	if !paymentNames["code_search"] || !paymentNames["code_read"] {
		t.Fatalf("expected payment agent code tools, got %#v", paymentNames)
	}
	legacyTools, err := registry.ToolsFor(ctx, ScenarioChat)
	if err != nil {
		t.Fatalf("load legacy chat tools: %v", err)
	}
	legacyNames := toolNames(t, ctx, legacyTools)
	if legacyNames["code_search"] || legacyNames["code_read"] {
		t.Fatalf("legacy registry API must not expose scoped code tools: %#v", legacyNames)
	}

	for _, scope := range []authz.AgentScope{
		authz.AgentScopeCoordinator,
		authz.AgentScopeObservability,
		authz.AgentScopeDatabase,
		authz.AgentScopeKnowledge,
	} {
		toolsForScope, scopeErr := registry.ToolsForAgent(ctx, ScenarioChat, scope)
		if scopeErr != nil {
			t.Fatalf("load %s tools: %v", scope, scopeErr)
		}
		names := toolNames(t, ctx, toolsForScope)
		if names["code_search"] || names["code_read"] {
			t.Fatalf("code tools leaked to %s: %#v", scope, names)
		}
	}
}

func TestCodeToolRuntimeAuthorizationRejectsWrongAgentScope(t *testing.T) {
	service := testCodeService(t)
	codeSearch, err := NewCodeSearchTool(service)
	if err != nil {
		t.Fatalf("create code search tool: %v", err)
	}
	wrapped := wrapAuthorizedTool(codeSearch)
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{
		UserID:   "reader",
		TenantID: "tenant-a",
		Roles:    []string{"code_reader"},
	})
	ctx = authz.WithAgentScope(ctx, authz.AgentScopeDatabase)
	ctx = context.WithValue(ctx, "client_id", "conversation-a")
	out, err := wrapped.InvokableRun(ctx, `{"query":"OpenPool","mode":"symbol"}`)
	if err != nil {
		t.Fatalf("invoke wrapped code tool: %v", err)
	}
	if !strings.Contains(out, "agent scope is not allowed") {
		t.Fatalf("expected scope denial, got %s", out)
	}

	ctx = authz.WithAgentScope(ctx, authz.AgentScopePayment)
	ctx = authz.WithAgentCapability(ctx, authz.NewAgentCapabilityWithCapabilities(authz.AgentScopePayment, time.Minute, "code.read"))
	out, err = wrapped.InvokableRun(ctx, `{"query":"OpenPool","mode":"symbol"}`)
	if err != nil {
		t.Fatalf("invoke allowed wrapped code tool: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("parse code tool envelope: %v", err)
	}
	if envelope["success"] != true {
		t.Fatalf("expected allowed code search, got %s", out)
	}
}

func TestCodeToolRuntimeAuthorizationRequiresCapabilityName(t *testing.T) {
	service := testCodeService(t)
	codeSearch, err := NewCodeSearchTool(service)
	if err != nil {
		t.Fatalf("create code search tool: %v", err)
	}
	wrapped := wrapAuthorizedTool(codeSearch)
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{
		UserID:   "reader",
		TenantID: "tenant-a",
		Roles:    []string{"code_reader"},
	})
	ctx = authz.WithAgentScope(ctx, authz.AgentScopePayment)
	ctx = authz.WithAgentCapability(ctx, authz.NewAgentCapability(authz.AgentScopePayment, time.Minute))

	out, err := wrapped.InvokableRun(ctx, `{"query":"OpenPool","mode":"symbol"}`)
	if err != nil {
		t.Fatalf("invoke code tool: %v", err)
	}
	if !strings.Contains(out, "agent capability is missing or expired") {
		t.Fatalf("expected capability-name denial, got %s", out)
	}
}

func testCodeService(t *testing.T) *codeintel.Service {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "internal", "pool.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create code test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("package pool\n\nfunc OpenPool() {}\n"), 0o644); err != nil {
		t.Fatalf("write code test file: %v", err)
	}
	service, err := codeintel.NewService(codeintel.Config{
		Enabled:          true,
		Repository:       codeintel.RepositoryName,
		ServiceName:      "card-service",
		Root:             root,
		Revision:         "rev-a",
		DeployedRevision: "rev-a",
		Include:          []string{"internal/**"},
		MaxSearchResults: 8,
		MaxReadLines:     80,
		MaxHandles:       16,
		HandleTTL:        time.Minute,
		HandleUses:       3,
	})
	if err != nil {
		t.Fatalf("build code service: %v", err)
	}
	return service
}
