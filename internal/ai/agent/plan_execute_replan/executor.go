package plan_execute_replan

import (
	"github.com/Thadopz/telltale/internal/ai/models"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/compose"
)

func NewExecutor(ctx context.Context) (adk.Agent, error) {
	toolList, err := tools.NewRegistry().ToolsFor(ctx, tools.ScenarioChat)
	if err != nil {
		return nil, err
	}
	execModel, err := models.OpenAIForDeepSeekV3Quick(ctx)
	if err != nil {
		return nil, err
	}
	return planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{
		Model: execModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: toolList,
			},
		},
		// Keep the internal planner bounded now that it is no longer exposed as
		// an unbounded standalone endpoint.
		MaxIterations: 50,
	})
}
