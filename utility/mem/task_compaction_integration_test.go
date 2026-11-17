package mem

import (
	"os"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/cloudwego/eino/schema"
)

func TestTaskCompactionRealProvider(t *testing.T) {
	if os.Getenv("TELLTALE_TEST_COMPACTION_PROVIDER") != "1" {
		t.Skip("requires explicit real provider invocation")
	}
	appconfig.ConfigureAt(t.Context(), "../..")
	memory := &SimpleMemory{MaxWindowSize: 8}
	memory.SetMessages(schema.UserMessage("排查服务A，order_id=ord-42。仅在测试环境操作，禁止重启。"))
	memory.SetMessages(schema.AssistantMessage("怀疑连接池耗尽，但尚未验证。", nil))
	memory.SetMessages(schema.UserMessage("纠正：目标是服务B，服务A的结论不适用。继续禁止重启。"))
	memory.SetMessages(schema.AssistantMessage("ev_pool_1 显示服务B连接充足，已排除连接池耗尽。PDP网络故障仍未知，下一步检查PDP日志。", nil))
	for range 10 {
		memory.SetMessages(schema.UserMessage("补充背景，无新诊断证据：" + strings.Repeat("这是历史背景材料。", 80)))
		memory.SetMessages(schema.AssistantMessage("收到背景，维持待查PDP的状态。", nil))
	}
	if err := memory.Compact(t.Context(), "目标服务B"); err != nil {
		t.Fatal(err)
	}
	view := memory.GetContextSummary(6000)
	for _, detail := range []string{"服务B", "禁止重启", "ord-42", "ev_pool_1", "PDP"} {
		if !strings.Contains(view, detail) {
			t.Fatalf("real compaction omitted %q", detail)
		}
	}
	if !strings.Contains(view, "rejected") {
		t.Fatal("rejected hypothesis lost")
	}
	t.Logf("real provider compaction: before=%d after=%d retained_messages=%d", memory.Compaction.BeforeTokens, memory.Compaction.AfterTokens, len(memory.Messages))
}
