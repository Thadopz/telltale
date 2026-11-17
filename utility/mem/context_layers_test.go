package mem

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestContextLayersIgnoreLegacyMemoryInEveryMode(t *testing.T) {
	ctx := t.Context()
	// 模拟升级后仍保留旧表的真实数据库。
	if _, err := g.DB().Exec(ctx, `CREATE TABLE IF NOT EXISTS durable_memories (user_id TEXT, kind TEXT, content TEXT, status TEXT, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	user := "legacy-context-regression"
	if _, err := g.DB().Model("durable_memories").Data(g.Map{"user_id": user, "kind": "preference", "content": "legacy-only-marker", "status": "active"}).Insert(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = g.DB().Model("durable_memories").Where("user_id", user).Delete() })
	if _, err := g.DB().Model("profile_slots").Data(g.Map{
		"tenant_id": "legacy-regression", "user_id": user, "slot_key": "preference.answer_detail",
		"value_type": "string", "value_json": `"concise"`, "value_text": "profile-only-marker",
		"revision": 1, "status": "active", "confirmation": "confirmed", "confidence": 1,
		"valid_from": time.Now().UTC(),
	}).Insert(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Model("profile_slots").Where("tenant_id", "legacy-regression").Where("user_id", user).Delete()
	})
	previous := g.Cfg().GetAdapter()
	t.Cleanup(func() { g.Cfg().SetAdapter(previous) })
	for _, mode := range []string{"off", "shadow", "active"} {
		t.Run(mode, func(t *testing.T) {
			adapter, err := gcfg.NewAdapterContent(fmt.Sprintf("memory:\n  mode: %s\n", mode))
			if err != nil {
				t.Fatal(err)
			}
			g.Cfg().SetAdapter(adapter)
			layers, err := LoadContextLayersForQuery(ctx, MemoryPrincipal{TenantID: "legacy-regression", UserID: user}, "", "preference")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(layers.DurableMemory, "legacy-only-marker") {
				t.Fatalf("unexpected memory context: %q", layers.DurableMemory)
			}
			if mode == "active" {
				if !strings.Contains(layers.DurableMemory, "profile-only-marker") {
					t.Fatalf("active mode lost profile context: %q", layers.DurableMemory)
				}
			} else if layers.DurableMemory != "" {
				t.Fatalf("inactive mode injected memory: %q", layers.DurableMemory)
			}
		})
	}
}
