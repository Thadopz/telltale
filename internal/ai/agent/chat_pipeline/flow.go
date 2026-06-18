package chat_pipeline

import (
	"SREagent/internal/ai/tools"
	"SREagent/internal/authz"
	"context"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
)

func newReactAgentLambdaWithOptions(ctx context.Context, opts ChatAgentOptions) (lba *compose.Lambda, err error) {
	maxStep := opts.ReactMaxStep
	if maxStep <= 0 {
		maxStep = defaultReactMaxStep(ctx)
	}
	config := &react.AgentConfig{
		MaxStep:            maxStep,
		ToolReturnDirectly: map[string]struct{}{}}
	chatModelIns11 := opts.ChatModel
	if chatModelIns11 == nil {
		chatModelIns11, err = newChatModel(ctx)
		if err != nil {
			return nil, err
		}
	}
	config.ToolCallingModel = chatModelIns11
	toolList := opts.Tools
	if toolList == nil {
		toolList, err = tools.NewRegistry().ToolsFor(ctx, tools.ScenarioChat)
		if err != nil {
			return nil, err
		}
	}
	config.ToolsConfig.Tools = toolList

	ins, err := react.NewAgent(ctx, config)
	if err != nil {
		return nil, err
	}
	lba, err = compose.AnyLambda(ins.Generate, ins.Stream, nil, nil)
	if err != nil {
		return nil, err
	}
	return lba, nil
}

func defaultReactMaxStep(ctx context.Context) int {
	principal := authz.PrincipalFromContext(ctx)
	for _, role := range principal.Roles {
		if strings.EqualFold(strings.TrimSpace(role), "sregym") {
			return 50
		}
	}
	return 25
}
