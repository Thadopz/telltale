package chat_pipeline

import (
	"SREagent/internal/ai/project_context"
	"SREagent/internal/ai/skills"
	"context"
	"time"
)

func newPrepareContextLambda(ctx context.Context, input *UserMessage) (*UserMessage, error) {
	if input == nil {
		return &UserMessage{ModelContext: modelContextPtr(modelContextForInput(ctx, nil))}, nil
	}
	prepared := *input
	modelContext := modelContextForInput(ctx, input)
	prepared.ModelContext = &modelContext
	return &prepared, nil
}

// newInputToChatLambda component initialization function of node 'InputToHistory' in graph 'EinoAgent'
func newInputToChatLambda(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
	modelContext := modelContextForInput(ctx, input)
	modelContext.ProjectContext = project_context.Load(ctx)
	modelContext.Skills = skills.LoadSelectedContext(ctx, modelContext.Content)
	return modelContext.PromptValues(), nil
}

func modelContextForInput(ctx context.Context, input *UserMessage) ModelContext {
	if input != nil && input.ModelContext != nil {
		return *input.ModelContext
	}
	return AssembleModelContext(ctx, input, time.Now())
}

func modelContextPtr(modelContext ModelContext) *ModelContext {
	return &modelContext
}
