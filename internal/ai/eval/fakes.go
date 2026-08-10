package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fixtureTool struct {
	fixture ToolFixture
}

type fixtureClaimExtractor struct {
	claimSet evidence.ClaimSet
}

func (extractor fixtureClaimExtractor) Extract(context.Context, chat_pipeline.ClaimExtractionInput) (evidence.ClaimSet, error) {
	return extractor.claimSet, nil
}

func (t fixtureTool) Info(context.Context) (*schema.ToolInfo, error) {
	description := strings.TrimSpace(t.fixture.Description)
	if description == "" {
		description = "Deterministic eval fixture for " + t.fixture.Name
	}
	return &schema.ToolInfo{
		Name:        t.fixture.Name,
		Desc:        description,
		ParamsOneOf: schema.NewParamsOneOfByParams(fixtureParameters(t.fixture.Parameters)),
	}, nil
}

func (t fixtureTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if t.fixture.Error != "" {
		return `{"success":false,"status":"error","error":"` + escapeJSONString(t.fixture.Error) + `"}`, nil
	}
	if t.fixture.Output != "" {
		parsed := evidence.Parse(t.fixture.Output)
		if parsed.IsJSON && parsed.Envelope.Status == evidence.StatusSuccess && len(parsed.Envelope.Observations) > 0 {
			parsed.Envelope = evidence.SanitizeUntrustedEnvelope(parsed.Envelope)
			parsed.Envelope = evidence.MarkLocalFixtureEnvelope(parsed.Envelope, evidence.IssueMetadata{
				Issuer:       "eval:fixture",
				RunID:        evidence.RunIDFromContext(ctx),
				TrustLevel:   evidence.ContentTrustTrustedLocal,
				ExpiresAfter: 10 * time.Minute,
			})
			return evidence.Marshal(parsed.Envelope), nil
		}
		return evidence.SanitizeUntrustedJSON(t.fixture.Output), nil
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

type contextStressModel struct {
	mu             sync.Mutex
	criticalFacts  []string
	currentQuery   string
	answer         string
	calls          int
	toolInfos      []*schema.ToolInfo
	missingFacts   []string
	seenFacts      []string
	querySeen      bool
	toolResultSeen bool
}

func newContextStressModel(criticalFacts []string, currentQuery string) *contextStressModel {
	return &contextStressModel{
		criticalFacts: append([]string(nil), criticalFacts...),
		currentQuery:  strings.TrimSpace(currentQuery),
		answer:        `Verified impact: external traffic normal. Evidence ev_payment_callback_history and ERR_PAYMENT_CALLBACK_TIMEOUT show failures on /api/v1/payment/callback. Most likely root cause: callback worker disabled. The worker-disabled observation is runtime evidence; the deployment cause remains a hypothesis. Next check: verify the worker deployment, queue health, and restart history.`,
	}
}

func (m *contextStressModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	content := messagesText(messages)
	missing := missingFacts(content, m.criticalFacts)
	if len(missing) > 0 {
		m.missingFacts = appendUniqueStrings(m.missingFacts, missing...)
		return nil, fmt.Errorf("context_stress_missing_fact: %s", strings.Join(missing, ", "))
	}
	m.seenFacts = appendUniqueStrings(m.seenFacts, m.criticalFacts...)
	if m.currentQuery != "" && strings.Contains(strings.ToLower(content), strings.ToLower(m.currentQuery)) {
		m.querySeen = true
	}
	for _, message := range messages {
		if message != nil && message.Role == schema.Tool && message.ToolName == "payment_agent" {
			m.toolResultSeen = true
		}
	}

	m.calls++
	if m.calls == 1 {
		arguments, err := json.Marshal(map[string]any{
			"query": "payment callback failure impact and evidence",
		})
		if err != nil {
			return nil, fmt.Errorf("marshal context stress tool arguments: %w", err)
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   fmt.Sprintf("context_stress_tool_%d", m.calls),
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "payment_agent",
				Arguments: string(arguments),
			},
		}}), nil
	}
	if !m.toolResultSeen {
		return nil, errors.New("context_stress_payment_tool_result_missing")
	}
	return schema.AssistantMessage(m.answer, nil), nil
}

func (m *contextStressModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (m *contextStressModel) WithTools(toolInfos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolInfos = append([]*schema.ToolInfo(nil), toolInfos...)
	return m, nil
}

func (m *contextStressModel) MissingFacts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.missingFacts...)
}

func (m *contextStressModel) SeenFacts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seenFacts...)
}

func (m *contextStressModel) QuerySeen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.querySeen
}

func (m *contextStressModel) ToolNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.toolInfos))
	for _, info := range m.toolInfos {
		if info != nil {
			names = append(names, info.Name)
		}
	}
	return names
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

func fixtureParameters(parameters map[string]ToolParameterFixture) map[string]*schema.ParameterInfo {
	if len(parameters) == 0 {
		return map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Desc: "Complete request, identifiers, and time range."},
		}
	}

	result := make(map[string]*schema.ParameterInfo, len(parameters))
	for name, parameter := range parameters {
		result[name] = &schema.ParameterInfo{
			Type:     fixtureParameterType(parameter.Type),
			Desc:     parameter.Description,
			Enum:     append([]string(nil), parameter.Enum...),
			Required: parameter.Required,
		}
	}
	return result
}

func fixtureParameterType(value string) schema.DataType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "boolean":
		return schema.Boolean
	case "integer":
		return schema.Integer
	case "number":
		return schema.Number
	case "array":
		return schema.Array
	case "object":
		return schema.Object
	default:
		return schema.String
	}
}

func messagesText(messages []*schema.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		if message == nil {
			continue
		}
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
		for _, call := range message.ToolCalls {
			builder.WriteString(call.Function.Name)
			builder.WriteString(" ")
			builder.WriteString(call.Function.Arguments)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func missingFacts(content string, facts []string) []string {
	lowerContent := strings.ToLower(content)
	var missing []string
	for _, fact := range facts {
		if !strings.Contains(lowerContent, strings.ToLower(fact)) {
			missing = append(missing, fact)
		}
	}
	return missing
}

func factsInText(content string, facts []string) []string {
	lowerContent := strings.ToLower(content)
	var found []string
	for _, fact := range facts {
		if strings.Contains(lowerContent, strings.ToLower(fact)) {
			found = append(found, fact)
		}
	}
	return found
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values
}

func escapeJSONString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
