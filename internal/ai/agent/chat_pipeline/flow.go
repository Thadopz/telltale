package chat_pipeline

import (
	"SREagent/internal/ai/tools"
	"SREagent/internal/authz"
	"SREagent/internal/observability"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type adkLambdaOption struct{}

type adkChatRunner struct {
	invokeRunner *adk.Runner
	streamRunner *adk.Runner
}

func newADKAgentLambdaWithOptions(ctx context.Context, opts ChatAgentOptions) (*compose.Lambda, error) {
	maxIterations := opts.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations(ctx)
	}

	chatModel := opts.ChatModel
	failoverModel := opts.FailoverModel
	var err error
	if chatModel == nil {
		chatModel, err = newChatModel(ctx)
		if err != nil {
			return nil, err
		}
		if failoverModel == nil {
			failoverModel, err = newFailoverChatModel(ctx)
			if err != nil {
				return nil, err
			}
		}
	}

	invokeAgent, err := newChatAgentGraph(ctx, chatModel, failoverModel, opts.Tools, maxIterations, defaultModelRetries(ctx))
	if err != nil {
		return nil, err
	}
	streamAgent, err := newChatAgentGraph(ctx, chatModel, failoverModel, opts.Tools, maxIterations, 0)
	if err != nil {
		return nil, err
	}

	runner := &adkChatRunner{
		invokeRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: invokeAgent}),
		streamRunner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: streamAgent, EnableStreaming: true}),
	}
	return compose.AnyLambda(runner.Invoke, runner.Stream, nil, nil)
}

type agentDefinition struct {
	name        string
	description string
	instruction string
}

var coordinatorDefinition = agentDefinition{
	name:        "sreagent_chat",
	description: "Evidence-driven SRE and OnCall coordinator",
}

var domainDefinitions = map[tools.Domain]agentDefinition{
	tools.DomainObservability: {
		name:        "observability_agent",
		description: "Inspect alerts, metrics, logs, traces, Kubernetes, and SREGym infrastructure evidence.",
		instruction: `You are the observability specialist. Use only available observability and infrastructure tools. For an unspecified time range, inspect the latest 30 minutes. Start with the strongest signal for the request, correlate evidence, and report facts, gaps, and safe next checks. Never invent tool results.`,
	},
	tools.DomainDatabase: {
		name:        "database_agent",
		description: "Inspect MySQL data and slow SQL evidence without crossing database safety policy.",
		instruction: `You are the database specialist. Prefer read-only inspection and slow-SQL evidence. Preserve identifiers exactly. Do not perform writes unless the existing authorization and confirmation policy explicitly permits them. Return findings, uncertainty, and validation steps.`,
	},
	tools.DomainPayment: {
		name:        "payment_agent",
		description: "Inspect payment and order anomalies, metrics, details, consistency, logs, and status distributions.",
		instruction: `You are the payment and order specialist. Preserve order, payment, callback, trace, and error identifiers exactly. Gather targeted evidence before broad aggregates. Correlate detail, consistency, metrics, logs, and status distribution as needed. Never infer success from a failed tool call.`,
	},
	tools.DomainKnowledge: {
		name:        "knowledge_agent",
		description: "Retrieve internal runbooks and technical documentation relevant to an incident.",
		instruction: `You are the internal knowledge specialist. Retrieve the most relevant runbooks and documentation, distinguish documentation from live state, and return concise cited guidance. Do not present a runbook statement as proof of current system behavior.`,
	},
}

func newChatAgentGraph(ctx context.Context, chatModel model.ToolCallingChatModel, failoverModel model.ToolCallingChatModel, customTools []tool.BaseTool, maxIterations int, modelRetries int) (*adk.ChatModelAgent, error) {
	if customTools != nil || !domainAgentsEnabled(ctx) {
		toolList := customTools
		if toolList == nil {
			var err error
			toolList, err = tools.NewRegistry().ToolsFor(ctx, tools.ScenarioChat)
			if err != nil {
				return nil, err
			}
		}
		return newConfiguredADKChatAgent(ctx, coordinatorDefinition, chatModel, failoverModel, toolList, maxIterations, modelRetries)
	}

	sets, err := tools.NewRegistry().ChatDomainTools(ctx)
	if err != nil {
		return nil, err
	}
	coordinatorTools, err := buildCoordinatorTools(ctx, chatModel, failoverModel, sets, maxIterations, modelRetries)
	if err != nil {
		return nil, err
	}
	return newConfiguredADKChatAgent(ctx, coordinatorDefinition, chatModel, failoverModel, coordinatorTools, maxIterations, modelRetries)
}

func buildCoordinatorTools(ctx context.Context, chatModel model.ToolCallingChatModel, failoverModel model.ToolCallingChatModel, sets []tools.DomainToolSet, maxIterations int, modelRetries int) ([]tool.BaseTool, error) {
	var coordinatorTools []tool.BaseTool
	for _, set := range sets {
		if set.Domain == tools.DomainGeneral {
			coordinatorTools = append(coordinatorTools, set.Tools...)
			continue
		}
		if len(set.Tools) == 0 {
			continue
		}

		definition, ok := domainDefinitions[set.Domain]
		if !ok {
			return nil, fmt.Errorf("missing agent definition for tool domain %q", set.Domain)
		}
		domainAgent, err := newConfiguredADKChatAgent(ctx, definition, chatModel, failoverModel, set.Tools, maxIterations, modelRetries)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", definition.name, err)
		}
		agentTool, err := newObservedDomainAgentTool(adk.NewAgentTool(ctx, domainAgent), definition.name, string(set.Domain))
		if err != nil {
			return nil, err
		}
		coordinatorTools = append(coordinatorTools, agentTool)
	}
	return coordinatorTools, nil
}

func newADKChatAgent(ctx context.Context, chatModel model.ToolCallingChatModel, failoverModel model.ToolCallingChatModel, toolList []tool.BaseTool, maxIterations int, modelRetries int) (*adk.ChatModelAgent, error) {
	return newConfiguredADKChatAgent(ctx, coordinatorDefinition, chatModel, failoverModel, toolList, maxIterations, modelRetries)
}

func newConfiguredADKChatAgent(ctx context.Context, definition agentDefinition, chatModel model.ToolCallingChatModel, failoverModel model.ToolCallingChatModel, toolList []tool.BaseTool, maxIterations int, modelRetries int) (*adk.ChatModelAgent, error) {
	config := &adk.ChatModelAgentConfig{
		Name:          definition.name,
		Description:   definition.description,
		Instruction:   definition.instruction,
		Model:         chatModel,
		MaxIterations: maxIterations,
	}

	if shouldUseToolSearch(ctx, len(toolList)) {
		toolSearchHandler, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: toolList})
		if err != nil {
			return nil, fmt.Errorf("create ADK tool search middleware: %w", err)
		}
		config.Handlers = []adk.ChatModelAgentMiddleware{toolSearchHandler}
	} else {
		config.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList},
		}
	}

	if modelRetries > 0 {
		config.ModelRetryConfig = &adk.ModelRetryConfig{MaxRetries: modelRetries}
	}
	if failoverModel != nil {
		config.ModelFailoverConfig = newModelFailoverConfig(failoverModel)
	}
	return adk.NewChatModelAgent(ctx, config)
}

func newModelFailoverConfig(failoverModel model.ToolCallingChatModel) *adk.ModelFailoverConfig[*schema.Message] {
	return &adk.ModelFailoverConfig[*schema.Message]{
		MaxRetries: 1,
		ShouldFailover: func(ctx context.Context, _ *schema.Message, modelErr error) bool {
			return modelErr != nil && ctx.Err() == nil
		},
		GetFailoverModel: func(_ context.Context, _ *adk.FailoverContext[*schema.Message]) (model.BaseChatModel, []*schema.Message, error) {
			return failoverModel, nil, nil
		},
	}
}

func (r *adkChatRunner) Invoke(ctx context.Context, messages []*schema.Message, _ ...adkLambdaOption) (*schema.Message, error) {
	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "agent.invoke",
		attribute.String("agent.name", coordinatorDefinition.name),
		attribute.String("agent.domain", "coordinator"),
		attribute.String("agent.kind", "coordinator"),
		attribute.Bool("agent.streaming", false),
	)
	var runErr error
	defer func() {
		elapsed := time.Since(startedAt)
		observability.RecordAgentRun(ctx, coordinatorDefinition.name, "coordinator", "coordinator", false, elapsed, runErr)
		observability.EndSpan(span, runErr, attribute.Float64("agent.duration_ms", float64(elapsed)/float64(time.Millisecond)))
	}()

	iterator := r.invokeRunner.Run(ctx, messages)
	var finalMessage *schema.Message

	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			runErr = event.Err
			return nil, event.Err
		}
		message, err := assistantMessageFromEvent(event)
		if err != nil {
			runErr = err
			return nil, err
		}
		if message != nil {
			finalMessage = message
		}
	}

	if finalMessage == nil {
		runErr = fmt.Errorf("ADK chat agent completed without an assistant message")
		return nil, runErr
	}
	return finalMessage, nil
}

func (r *adkChatRunner) Stream(ctx context.Context, messages []*schema.Message, _ ...adkLambdaOption) (*schema.StreamReader[*schema.Message], error) {
	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "agent.invoke",
		attribute.String("agent.name", coordinatorDefinition.name),
		attribute.String("agent.domain", "coordinator"),
		attribute.String("agent.kind", "coordinator"),
		attribute.Bool("agent.streaming", true),
	)
	reader, writer := schema.Pipe[*schema.Message](1)
	iterator := r.streamRunner.Run(ctx, messages)

	go forwardADKStream(ctx, iterator, writer, span, startedAt)
	return reader, nil
}

func forwardADKStream(ctx context.Context, iterator *adk.AsyncIterator[*adk.AgentEvent], writer *schema.StreamWriter[*schema.Message], span oteltrace.Span, startedAt time.Time) {
	defer writer.Close()
	var streamErr error
	defer func() {
		elapsed := time.Since(startedAt)
		observability.RecordAgentRun(ctx, coordinatorDefinition.name, "coordinator", "coordinator", true, elapsed, streamErr)
		observability.EndSpan(span, streamErr, attribute.Float64("agent.duration_ms", float64(elapsed)/float64(time.Millisecond)))
	}()

	for {
		event, ok := iterator.Next()
		if !ok {
			return
		}
		if event.Err != nil {
			streamErr = event.Err
			writer.Send(nil, event.Err)
			return
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		messageOutput := event.Output.MessageOutput
		if messageOutput.Role != schema.Assistant {
			continue
		}
		if !messageOutput.IsStreaming {
			if messageOutput.Message != nil && writer.Send(messageOutput.Message, nil) {
				return
			}
			continue
		}

		forwardErr, stopped := forwardMessageStream(messageOutput.MessageStream, writer)
		if forwardErr != nil {
			streamErr = forwardErr
		}
		if stopped {
			return
		}
	}
}

func forwardMessageStream(stream *schema.StreamReader[*schema.Message], writer *schema.StreamWriter[*schema.Message]) (error, bool) {
	if stream == nil {
		return nil, false
	}
	defer stream.Close()

	for {
		message, err := stream.Recv()
		if err == io.EOF {
			return nil, false
		}
		if err != nil {
			writer.Send(nil, err)
			return err, true
		}
		if message != nil && writer.Send(message, nil) {
			return nil, true
		}
	}
}

func assistantMessageFromEvent(event *adk.AgentEvent) (*schema.Message, error) {
	if event == nil || event.Output == nil || event.Output.MessageOutput == nil {
		return nil, nil
	}
	messageOutput := event.Output.MessageOutput
	if messageOutput.Role != schema.Assistant {
		return nil, nil
	}
	return messageOutput.GetMessage()
}

func defaultMaxIterations(ctx context.Context) int {
	principal := authz.PrincipalFromContext(ctx)
	for _, role := range principal.Roles {
		if strings.EqualFold(strings.TrimSpace(role), "sregym") {
			return 50
		}
	}
	return 25
}

func defaultModelRetries(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "agent.invoke_model_retries")
	if err != nil || value == nil {
		return 1
	}
	retries := value.Int()
	if retries < 0 {
		return 0
	}
	return retries
}

func shouldUseToolSearch(ctx context.Context, toolCount int) bool {
	enabled, err := g.Cfg().Get(ctx, "agent.tool_search.enabled")
	if err == nil && enabled != nil && !enabled.Bool() {
		return false
	}

	minimum := 8
	configuredMinimum, configErr := g.Cfg().Get(ctx, "agent.tool_search.min_tools")
	if configErr == nil && configuredMinimum != nil && configuredMinimum.Int() > 0 {
		minimum = configuredMinimum.Int()
	}
	return toolCount >= minimum
}

func domainAgentsEnabled(ctx context.Context) bool {
	enabled, err := g.Cfg().Get(ctx, "agent.domain_agents.enabled")
	return err != nil || enabled == nil || enabled.Bool()
}
