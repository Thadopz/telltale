package execution

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/project_context"
	"github.com/Thadopz/telltale/internal/ai/skills"
	"time"
)

func newPrepareContextLambda(ctx context.Context, input *UserMessage) (*UserMessage, error) {
	return newPrepareContextLambdaWithPlan(defaultContextBudgetPlan(ctx))(ctx, input)
}

func newPrepareContextLambdaWithPlan(plan contextBudgetPlan) func(context.Context, *UserMessage) (*UserMessage, error) {
	return func(ctx context.Context, input *UserMessage) (*UserMessage, error) {
		if input == nil {
			modelContext := modelContextForInput(ctx, nil)
			modelContext, err := budgetModelContextWithPlan(ctx, modelContext, plan)
			if err != nil {
				return nil, err
			}
			return &UserMessage{ModelContext: modelContextPtr(modelContext)}, nil
		}
		prepared := *input
		modelContext := modelContextForInput(ctx, input)
		modelContext, err := budgetModelContextWithPlan(ctx, modelContext, plan)
		if err != nil {
			return nil, err
		}
		prepared.ModelContext = &modelContext
		return &prepared, nil
	}
}

// newInputToChatLambda component initialization function of node 'InputToHistory' in graph 'EinoAgent'
func newInputToChatLambda(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
	return newInputToChatLambdaWithPlan(defaultContextBudgetPlan(ctx))(ctx, input, opts...)
}

func newInputToChatLambdaWithPlan(plan contextBudgetPlan) func(context.Context, *UserMessage, ...any) (map[string]any, error) {
	return func(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
		modelContext := modelContextForInput(ctx, input)
		modelContext.ProjectContext = project_context.Load(ctx)
		modelContext.Skills = skills.LoadSelectedContext(ctx, modelContext.Content)
		modelContext, err = budgetModelContextWithPlan(ctx, modelContext, plan)
		if err != nil {
			return nil, err
		}
		return modelContext.PromptValues(), nil
	}
}

func modelContextForInput(ctx context.Context, input *UserMessage) ModelContext {
	if input != nil && input.ModelContext != nil {
		return *input.ModelContext
	}
	return assembleRawModelContext(ctx, input, time.Now())
}

func modelContextPtr(modelContext ModelContext) *ModelContext {
	return &modelContext
}
