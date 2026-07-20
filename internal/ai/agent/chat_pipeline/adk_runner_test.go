package chat_pipeline

import (
	"SREagent/internal/ai/tools"
	"context"
	"errors"
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

func TestChatPipelineSendsOneSystemMessageToCoordinator(t *testing.T) {
	ctx := context.Background()
	chatModel := &testADKModel{}
	runner, err := BuildChatAgentWithOptions(ctx, ChatAgentOptions{
		ChatModel:     chatModel,
		Tools:         []tool.BaseTool{},
		SkipRerank:    true,
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
	if systemMessages != 1 {
		t.Fatalf("expected one system message, got %d in %#v", systemMessages, chatModel.generatedMessages)
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
		{Domain: tools.DomainPayment, Tools: []tool.BaseTool{namedTestTool{name: "leaf_payments"}}},
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

	for _, expected := range []string{"clock", "observability_agent", "payment_agent"} {
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
