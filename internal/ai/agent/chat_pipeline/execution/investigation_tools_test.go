package execution

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/codeintel"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/tool"
)

func TestInvestigationToolBoundaries(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	service, err := codeintel.NewService(codeintel.Config{
		Enabled:     true,
		Repository:  codeintel.RepositoryName,
		ServiceName: "telltale",
		Root:        root,
		Revision:    "working-tree",
		Include:     []string{"*.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := authz.WithPrincipal(t.Context(), authz.Principal{UserID: "reader", Roles: []string{"oncall", "code_reader"}})
	registry := tools.NewRegistryWithCodeService(service)
	databaseOnly, err := investigationTools(ctx, registry, tools.DomainObservability, []tools.Domain{tools.DomainDatabase}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !investigationDomainAllowed(tools.DomainObservability, []tools.Domain{tools.DomainDatabase}) || len(databaseOnly) == 0 {
		t.Fatal("database-only investigation is no longer routable")
	}
	for _, candidate := range databaseOnly {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(info.Name, "db_") {
			t.Fatal("database-only request exposed another tool", info.Name)
		}
	}
	for _, domain := range []tools.Domain{tools.DomainObservability, tools.DomainKnowledge} {
		list, err := investigationTools(ctx, registry, domain, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, candidate := range list {
			info, err := candidate.Info(ctx)
			if err != nil {
				t.Fatal(err)
			}
			names[info.Name] = true
			if domain == tools.DomainKnowledge && info.Name == "code_search" {
				callContext := authz.WithAgentScope(ctx, authz.AgentScopeKnowledge)
				output, err := candidate.(tool.InvokableTool).InvokableRun(callContext, `{"query":"configuredAgentScope"}`)
				if err != nil {
					t.Fatal(err)
				}
				if evidence.Parse(output).Envelope.Status != evidence.StatusSuccess {
					t.Fatal("mechanism code query failed through the original authorization boundary", output)
				}
				deniedContext := authz.WithPrincipal(callContext, authz.Principal{UserID: "denied", Roles: []string{"unknown"}})
				output, err = candidate.(tool.InvokableTool).InvokableRun(deniedContext, `{"query":"configuredAgentScope"}`)
				if err != nil {
					t.Fatal(err)
				}
				if evidence.Parse(output).Envelope.Status == evidence.StatusSuccess {
					t.Fatal("retained tool bypassed runtime principal authorization")
				}
			}
		}
		if domain == tools.DomainObservability {
			if names["code_search"] || names["code_read"] || !names["db_slow_queries"] {
				t.Fatal("runtime investigation tools are misplaced", names)
			}
		} else if !names["code_search"] || !names["code_read"] || !names["query_internal_docs"] || names["db_slow_queries"] {
			t.Fatal("mechanism investigation tools are misplaced", names)
		}
	}
	denied := authz.WithPrincipal(t.Context(), authz.Principal{UserID: "denied", Roles: []string{"unknown"}})
	list, err := investigationTools(denied, tools.NewRegistryWithCodeService(service), tools.DomainKnowledge, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range list {
		info, err := candidate.Info(denied)
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == "code_search" || info.Name == "code_read" {
			t.Fatal("reassigned tools bypassed the original principal permissions")
		}
	}
}
