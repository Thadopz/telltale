package execution

import (
	"context"
	"errors"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/authz"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type testADKModel struct {
	generateCalls     int
	streamCalls       int
	failFirst         bool
	streamErr         error
	generatedMessages []*schema.Message
}

func (m *testADKModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.generateCalls++
	m.generatedMessages = cloneHistory(messages)
	if m.failFirst && m.generateCalls == 1 {
		return nil, errors.New("temporary model failure")
	}
	return schema.AssistantMessage("diagnosis complete", nil), nil
}

func TestChatPipelineKeepsDynamicSystemMessageAfterStablePrefix(t *testing.T) {
	ctx := context.Background()
	chatModel := &testADKModel{}
	runner, err := BuildChatAgentWithOptions(ctx, ChatAgentOptions{
		ChatModel:     chatModel,
		Tools:         []tool.BaseTool{},
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("build chat pipeline: %v", err)
	}

	_, err = runner.Invoke(ctx, &UserMessage{Query: "hello"})
	if err != nil {
		t.Fatalf("invoke chat pipeline: %v", err)
	}

	systemMessages := 0
	for _, message := range chatModel.generatedMessages {
		if message != nil && message.Role == schema.System {
			systemMessages++
		}
	}
	if systemMessages != 2 {
		t.Fatalf("expected stable and dynamic system messages, got %d in %#v", systemMessages, chatModel.generatedMessages)
	}
	if len(chatModel.generatedMessages) < 2 || chatModel.generatedMessages[0].Extra[dynamicContextMessageKey] == true {
		t.Fatalf("first system message must be the stable prefix: %#v", chatModel.generatedMessages)
	}
	if dynamic, _ := chatModel.generatedMessages[1].Extra[dynamicContextMessageKey].(bool); !dynamic {
		t.Fatalf("second system message must contain late turn context: %#v", chatModel.generatedMessages)
	}
}

func (m *testADKModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return schema.StreamReaderFromArray([]*schema.Message{
		schema.AssistantMessage("diagnosis ", nil),
		schema.AssistantMessage("complete", nil),
	}), nil
}

func (m *testADKModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type namedTestTool struct {
	name string
}

func (t namedTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "test tool"}, nil
}

func (t namedTestTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

func TestCoordinatorExposesDomainAgentsInsteadOfLeafTools(t *testing.T) {
	ctx := context.Background()
	chatModel := &testADKModel{}
	sets := []tools.DomainToolSet{
		{Domain: tools.DomainGeneral, Tools: []tool.BaseTool{namedTestTool{name: "clock"}}},
		{Domain: tools.DomainObservability, Tools: []tool.BaseTool{namedTestTool{name: "leaf_alerts"}}},
		{Domain: tools.DomainDatabase},
		{Domain: tools.DomainKnowledge, Tools: []tool.BaseTool{namedTestTool{name: "leaf_knowledge"}}},
	}

	coordinatorTools, err := buildCoordinatorTools(ctx, chatModel, nil, sets, 3, 0)
	if err != nil {
		t.Fatalf("build coordinator tools: %v", err)
	}

	names := make(map[string]bool, len(coordinatorTools))
	for _, candidate := range coordinatorTools {
		info, infoErr := candidate.Info(ctx)
		if infoErr != nil {
			t.Fatalf("load coordinator tool info: %v", infoErr)
		}
		names[info.Name] = true
	}

	for _, expected := range []string{"clock", "observability_agent", "mechanism_agent"} {
		if !names[expected] {
			t.Fatalf("expected coordinator tool %s, got %#v", expected, names)
		}
	}
	for _, hidden := range []string{"leaf_alerts", "leaf_payments", "database_agent"} {
		if names[hidden] {
			t.Fatalf("expected coordinator not to expose %s, got %#v", hidden, names)
		}
	}
}

func TestCoordinatorRegistryExposesOnlyFunctionalAgents(t *testing.T) {
	ctx := authz.WithPrincipal(t.Context(), authz.Principal{UserID: "ops", Roles: []string{"oncall"}})
	registered, _, err := buildCoordinatorToolsFromRegistry(ctx, tools.NewRegistry(), &testADKModel{}, nil, 3, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, candidate := range registered {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = true
	}
	for _, name := range []string{"observability_agent", "mechanism_agent"} {
		if !names[name] {
			t.Fatal("missing functional agent", name)
		}
	}
	if len(domainDefinitions) != 2 || names["database_agent"] || names["knowledge_agent"] || names["payment_agent"] || names["query_payment_metrics"] {
		t.Fatal("coordinator exposed a business agent or leaf tool", names)
	}
}

func TestADKChatRunnerRetriesTransientModelFailure(t *testing.T) {
	ctx := context.Background()
	chatModel := &testADKModel{failFirst: true}
	agent, err := newADKChatAgent(ctx, chatModel, nil, nil, 3, 1)
	if err != nil {
		t.Fatalf("create ADK agent: %v", err)
	}
	runner := &adkChatRunner{
		invokeRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent}),
	}

	message, err := runner.Invoke(ctx, []*schema.Message{schema.UserMessage("check service")})
	if err != nil {
		t.Fatalf("invoke ADK agent: %v", err)
	}
	if message.Content != "diagnosis complete" {
		t.Fatalf("unexpected answer: %s", message.Content)
	}
	if chatModel.generateCalls != 2 {
		t.Fatalf("expected one retry, got %d model calls", chatModel.generateCalls)
	}
}

func TestADKChatRunnerPreservesStreamingChunks(t *testing.T) {
	ctx := context.Background()
	chatModel := &testADKModel{}
	agent, err := newADKChatAgent(ctx, chatModel, nil, nil, 3, 0)
	if err != nil {
		t.Fatalf("create ADK agent: %v", err)
	}
	runner := &adkChatRunner{
		streamRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true}),
	}

	stream, err := runner.Stream(ctx, []*schema.Message{schema.UserMessage("check service")})
	if err != nil {
		t.Fatalf("stream ADK agent: %v", err)
	}
	defer stream.Close()

	var answer strings.Builder
	for {
		message, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			t.Fatalf("receive ADK stream: %v", receiveErr)
		}
		answer.WriteString(message.Content)
	}
	if answer.String() != "diagnosis complete" {
		t.Fatalf("unexpected streamed answer: %s", answer.String())
	}
	if chatModel.streamCalls != 1 {
		t.Fatalf("expected one streaming model call, got %d", chatModel.streamCalls)
	}
}

func TestADKChatRunnerFailsOverAfterPrimaryModelError(t *testing.T) {
	ctx := context.Background()
	primaryModel := &testADKModel{failFirst: true}
	failoverModel := &testADKModel{}
	agent, err := newADKChatAgent(ctx, primaryModel, failoverModel, nil, 3, 0)
	if err != nil {
		t.Fatalf("create ADK agent: %v", err)
	}
	runner := &adkChatRunner{
		invokeRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent}),
	}

	message, err := runner.Invoke(ctx, []*schema.Message{schema.UserMessage("check service")})
	if err != nil {
		t.Fatalf("invoke ADK agent: %v", err)
	}
	if message.Content != "diagnosis complete" {
		t.Fatalf("unexpected answer: %s", message.Content)
	}
	if primaryModel.generateCalls != 1 {
		t.Fatalf("expected one primary model call, got %d", primaryModel.generateCalls)
	}
	if failoverModel.generateCalls != 1 {
		t.Fatalf("expected one failover model call, got %d", failoverModel.generateCalls)
	}
}

func TestADKChatRunnerFailsOverWhenPrimaryStreamCannotStart(t *testing.T) {
	ctx := context.Background()
	primaryModel := &testADKModel{streamErr: errors.New("primary stream unavailable")}
	failoverModel := &testADKModel{}
	agent, err := newADKChatAgent(ctx, primaryModel, failoverModel, nil, 3, 0)
	if err != nil {
		t.Fatalf("create ADK agent: %v", err)
	}
	runner := &adkChatRunner{
		streamRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true}),
	}

	stream, err := runner.Stream(ctx, []*schema.Message{schema.UserMessage("check service")})
	if err != nil {
		t.Fatalf("stream ADK agent: %v", err)
	}
	defer stream.Close()

	var answer strings.Builder
	for {
		message, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			t.Fatalf("receive ADK stream: %v", receiveErr)
		}
		answer.WriteString(message.Content)
	}
	if answer.String() != "diagnosis complete" {
		t.Fatalf("unexpected streamed answer: %s", answer.String())
	}
	if primaryModel.streamCalls != 1 || failoverModel.streamCalls != 1 {
		t.Fatalf("expected one stream call per model, got primary=%d failover=%d", primaryModel.streamCalls, failoverModel.streamCalls)
	}
}
