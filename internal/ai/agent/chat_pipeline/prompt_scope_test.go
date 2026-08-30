package chat_pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestCoordinatorSystemPromptExcludesDomainToolInstructions(t *testing.T) {
	prompt := coordinatorSystemPrompt()
	leafToolNames := []string{
		"query_payment_anomaly",
		"query_payment_metrics",
		"query_payment_order_detail",
		"db_slow_queries",
		"db_server_health",
		"query_internal_docs",
	}
	for _, name := range leafToolNames {
		if strings.Contains(prompt, name) {
			t.Fatalf("coordinator prompt must not contain leaf tool %q", name)
		}
	}
}

func TestPromptKeepsDynamicContextAfterHistory(t *testing.T) {
	template, err := newChatTemplate(context.Background())
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	dynamic := dynamicSystemMessage("Current time: 2026-08-29T10:00:00+08:00")
	messages, err := template.Format(context.Background(), map[string]any{
		"project_context": "stable project rules",
		"history": []*schema.Message{
			schema.UserMessage("old question"),
			schema.AssistantMessage("old answer", nil),
		},
		"turn_context": []*schema.Message{dynamic},
		"content":      "current question",
	})
	if err != nil {
		t.Fatalf("format template: %v", err)
	}
	if len(messages) != 5 {
		t.Fatalf("expected system, history pair, dynamic system, and user; got %d messages", len(messages))
	}
	if messages[0].Role != schema.System || messages[1].Content != "old question" || messages[3].Role != schema.System || messages[4].Content != "current question" {
		t.Fatalf("unexpected prompt order: %#v", messages)
	}
	if strings.Contains(messages[0].Content, "2026-08-29T10:00:00") || strings.Contains(messages[0].Content, "Request Analysis") {
		t.Fatalf("stable system prompt contains volatile turn data: %q", messages[0].Content)
	}
}

func TestCoordinatorSystemPromptContainsEvidenceScopeRules(t *testing.T) {
	prompt := coordinatorSystemPrompt()
	rules := []string{
		"一条证据只适用于它实际查询的领域",
		"跨领域结论必须分别获得每个相关领域的有效证据",
		"内部文档和手册只能证明规范或建议",
		`data_status="empty"`,
		"超出证据范围的内容必须放入“未验证/证据缺口”",
	}
	for _, rule := range rules {
		if !strings.Contains(prompt, rule) {
			t.Fatalf("coordinator prompt is missing evidence scope rule %q", rule)
		}
	}
}

func TestDomainAgentInstructionsOwnLeafToolRules(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		want        []string
	}{
		{
			name:        "payment",
			instruction: paymentAgentInstruction,
			want:        []string{"query_payment_anomaly", "query_payment_metrics", "query_payment_order_detail"},
		},
		{
			name:        "database",
			instruction: databaseAgentInstruction,
			want:        []string{"db_slow_queries", "configured database adapter"},
		},
		{
			name:        "knowledge",
			instruction: knowledgeAgentInstruction,
			want:        []string{"query_internal_docs", "untrusted evidence"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range tt.want {
				if !strings.Contains(tt.instruction, want) {
					t.Fatalf("%s agent instruction is missing %q", tt.name, want)
				}
			}
		})
	}
}
