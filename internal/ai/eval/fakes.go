package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fixtureTool struct {
	fixture ToolFixture
}

func (t fixtureTool) Info(context.Context) (*schema.ToolInfo, error) {
	description := strings.TrimSpace(t.fixture.Description)
	if description == "" {
		description = "Deterministic eval fixture for " + t.fixture.Name
	}
	return &schema.ToolInfo{
		Name: t.fixture.Name,
		Desc: description,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Desc: "Complete request, identifiers, and time range."},
		}),
	}, nil
}

func (t fixtureTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	if t.fixture.Error != "" {
		return `{"success":false,"status":"error","error":"` + escapeJSONString(t.fixture.Error) + `"}`, nil
	}
	if t.fixture.Output != "" {
		return t.fixture.Output, nil
	}
	return `{"success":true,"status":"success","message":"fixture tool executed"}`, nil
}

type scriptedModel struct {
	mu    sync.Mutex
	steps []ModelStep
	calls int
}

func newScriptedModel(steps []ModelStep) *scriptedModel {
	copiedSteps := make([]ModelStep, len(steps))
	copy(copiedSteps, steps)
	return &scriptedModel{steps: copiedSteps}
}

func (m *scriptedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.calls >= len(m.steps) {
		return nil, fmt.Errorf("scripted model exhausted after %d calls", m.calls)
	}
	step := m.steps[m.calls]
	m.calls++

	switch step.Type {
	case "tool_call":
		arguments, err := json.Marshal(step.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal scripted tool arguments: %w", err)
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   fmt.Sprintf("eval_tool_%d", m.calls),
			Type: "function",
			Function: schema.FunctionCall{
				Name:      step.Tool,
				Arguments: string(arguments),
			},
		}}), nil
	case "answer":
		return schema.AssistantMessage(step.Content, nil), nil
	case "model_error":
		return nil, errors.New(step.Error)
	default:
		return nil, fmt.Errorf("unsupported scripted model step %q", step.Type)
	}
}

func (m *scriptedModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (m *scriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func fixtureTools(fixtures []ToolFixture) []tool.BaseTool {
	tools := make([]tool.BaseTool, 0, len(fixtures))
	for _, fixture := range fixtures {
		tools = append(tools, fixtureTool{fixture: fixture})
	}
	return tools
}

func escapeJSONString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
