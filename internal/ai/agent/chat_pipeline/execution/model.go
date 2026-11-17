package execution

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/models"

	"github.com/cloudwego/eino/components/model"
	"github.com/gogf/gf/v2/frame/g"
)

func newChatModel(ctx context.Context) (cm model.ToolCallingChatModel, err error) {
	cm, err = models.OpenAIForDeepSeekV3Quick(ctx)
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func newFailoverChatModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	enabled, err := g.Cfg().Get(ctx, "agent.failover.enabled")
	if err != nil || enabled == nil || !enabled.Bool() {
		return nil, nil
	}

	chatModel, err := models.OpenAIForFailover(ctx)
	if err != nil {
		return nil, fmt.Errorf("load failover chat model: %w", err)
	}
	return chatModel, nil
}
