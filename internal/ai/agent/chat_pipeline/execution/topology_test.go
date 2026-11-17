package execution

import (
	"testing"

	"github.com/Thadopz/telltale/internal/authz"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestConfiguredAgentTopology(t *testing.T) {
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() {
		g.Cfg().SetAdapter(previous)
	})
	var multiKey string
	for _, topology := range []string{"multi", "single", "invalid"} {
		adapter, err := gcfg.NewAdapterContent("agent:\n  topology: " + topology + "\n")
		if err != nil {
			t.Fatal(err)
		}
		g.Cfg().SetAdapter(adapter)
		scope, err := configuredAgentScope(t.Context(), "")
		if topology == "invalid" {
			if err == nil {
				t.Fatal("invalid topology accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		key := NewChatAgentFactory().cacheKey(t.Context())
		if topology == "multi" {
			multiKey = key
			if scope != "" {
				t.Fatal("default multi agent scope changed")
			}
		} else if scope != authz.AgentScopeObservability || key == multiKey {
			t.Fatal("single agent scope or graph cache is incorrect")
		}
	}
}

func TestSingleAgentRuntimeScope(t *testing.T) {
	runner := &adkChatRunner{directScope: authz.AgentScopeObservability}
	ctx := runner.withDirectAgentScope(t.Context())
	if authz.AgentScopeFromContext(ctx) != authz.AgentScopeObservability {
		t.Fatal("single agent lost its runtime authorization scope")
	}
	multi := &adkChatRunner{}
	if multi.withDirectAgentScope(t.Context()) != t.Context() {
		t.Fatal("multi agent runtime context changed")
	}
}
