package mem

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

// 直接验证状态提交与持久化，不模拟模型生成质量。
func commitQuotedPrefix(t *testing.T, memory *SimpleMemory, cut int) {
	t.Helper()
	sources := compactionSources(memory.Summary, memory.Messages[:cut])
	state := taskState{}
	for _, source := range sources {
		state.Entries = append(state.Entries, taskEntry{Kind: "finding", Status: "reported", Text: source.Quote, Sources: []taskSource{source}})
	}
	if err := memory.commitTaskState(state, cut); err != nil {
		t.Fatal(err)
	}
}

func TestCompactionStateValidationPreservesCorrectionsAndConditions(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("仅在测试环境操作，禁止重启。order_id=ord-42"),
		schema.AssistantMessage("曾怀疑连接池耗尽；ev_pool_1 显示连接充足，已排除。", nil),
		schema.UserMessage("纠正：排查服务B，之前服务A的结论不适用。"),
	}
	sources := compactionSources("", messages)
	state := taskState{Entries: []taskEntry{
		{Kind: "constraint", Status: "current", Text: messages[0].Content, Sources: []taskSource{sources[0]}},
		{Kind: "hypothesis", Status: "rejected", Text: messages[1].Content, Sources: []taskSource{sources[1]}},
		{Kind: "goal", Status: "current", Text: messages[2].Content, Sources: []taskSource{sources[2]}},
	}}
	raw, _ := json.Marshal(state)
	if _, err := validateTaskState(string(raw), sources, 4000); err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Sources[0].Quote = "允许重启"
	raw, _ = json.Marshal(state)
	if _, err := validateTaskState(string(raw), sources, 4000); err == nil {
		t.Fatal("altered quotation accepted")
	}
	if _, err := validateTaskState(`{"entries":[]}`, sources, 4000); err == nil {
		t.Fatal("empty state accepted")
	}
}

func TestCompactionArchivesRawTurnsAndRestoresCheckpoint(t *testing.T) {
	id := "task-compact-" + time.Now().Format("150405.000000000")
	memory := &SimpleMemory{ID: id, UserID: "task-user", MaxWindowSize: 4, store: newSQLiteMemoryStore()}
	for _, text := range []string{"original order_id=ord-42", "hypothesis rejected ev_pool_1", "current goal", "current answer"} {
		role := schema.User
		if len(memory.Messages)%2 == 1 {
			role = schema.Assistant
		}
		memory.SetMessages(&schema.Message{Role: role, Content: text})
	}
	commitQuotedPrefix(t, memory, 2)
	count, err := g.DB().Model("conversation_turns").Where("conversation_id", id).Count()
	if err != nil || count != 4 {
		t.Fatalf("raw turns changed: %d %v", count, err)
	}
	snapshot, err := newSQLiteMemoryStore().Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 || snapshot.Messages[0].Content != "current goal" {
		t.Fatalf("wrong checkpoint: %#v", snapshot.Messages)
	}
	if !json.Valid([]byte(snapshot.Summary)) || !strings.Contains(snapshot.Summary, "ord-42") {
		t.Fatal("lost state")
	}
	if !containsFact(snapshot.Facts, "order_id=ord-42") {
		t.Fatal("lost exact identifier")
	}
	reloaded := newSimpleMemory("task-user", id, newSQLiteMemoryStore())
	reloaded.SetMessages(schema.UserMessage("new goal"))
	reloaded.SetMessages(schema.AssistantMessage("new answer", nil))
	commitQuotedPrefix(t, reloaded, 2)
	snapshot, err = newSQLiteMemoryStore().Load(id)
	if err != nil || len(snapshot.Messages) != 2 || snapshot.Messages[0].Content != "new goal" {
		t.Fatalf("second checkpoint failed: %#v %v", snapshot, err)
	}
}

func TestCompactionFailureLeavesHistoryIntact(t *testing.T) {
	memory := &SimpleMemory{ID: "failure", MaxWindowSize: 2, store: failingMemoryStore{}}
	memory.Messages = []*schema.Message{schema.UserMessage("old"), schema.AssistantMessage("answer", nil), schema.UserMessage("new"), schema.AssistantMessage("latest", nil)}
	state := taskState{Entries: []taskEntry{{Kind: "goal", Status: "current", Text: "old"}}}
	if err := memory.commitTaskState(state, 2); err == nil {
		t.Fatal("storage failure ignored")
	}
	if len(memory.Messages) != 4 || memory.Summary != "" || memory.Compaction.Generation != 0 {
		t.Fatal("failed commit changed history")
	}
}

func TestTaskSummaryNeverSlicesARecord(t *testing.T) {
	state := taskState{Version: 1, Entries: []taskEntry{{Kind: "constraint", Status: "current", Text: strings.Repeat("条件", 100) + "禁止重启"}}}
	raw, _ := json.Marshal(state)
	memory := &SimpleMemory{Summary: string(raw)}
	view := memory.GetContextSummary(150)
	if strings.Contains(view, "条件") || !strings.Contains(view, "omitted") {
		t.Fatalf("partial record exposed: %s", view)
	}
}

func TestDynamicCompactionMaxTokensIsBounded(t *testing.T) {
	limit := DynamicCompactionMaxTokens(t.Context())
	if limit < defaultDynamicCompactionMinTokens || limit > defaultDynamicCompactionMaxTokens {
		t.Fatalf("invalid dynamic budget %d", limit)
	}
}

func containsFact(facts []ContextFact, value string) bool {
	for _, fact := range facts {
		if fact.Value == value {
			return true
		}
	}
	return false
}
