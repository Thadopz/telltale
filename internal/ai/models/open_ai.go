package models

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/gogf/gf/v2/frame/g"
)

func OpenAIForDeepSeekV31Think(ctx context.Context) (cm model.ToolCallingChatModel, err error) {
	config, err := configuredOpenAIChatModel(ctx, "think_chat_model")
	if err != nil {
		return nil, err
	}
	cm, err = openai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}
	return wrapProviderCacheModel(ctx, cm, config.Model)
}

func OpenAIForDeepSeekV3Quick(ctx context.Context) (cm model.ToolCallingChatModel, err error) {
	config, err := configuredOpenAIChatModel(ctx, "quick_chat_model")
	if err != nil {
		return nil, err
	}
	cm, err = openai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}
	return wrapProviderCacheModel(ctx, cm, config.Model)
}

func OpenAIForFailover(ctx context.Context) (model.ToolCallingChatModel, error) {
	config, err := configuredOpenAIChatModel(ctx, "failover_chat_model")
	if err != nil {
		return nil, err
	}

	chatModel, err := openai.NewChatModel(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create failover chat model: %w", err)
	}
	return wrapProviderCacheModel(ctx, chatModel, config.Model)
}

func configuredOpenAIChatModel(ctx context.Context, configKey string) (*openai.ChatModelConfig, error) {
	modelName, err := requiredModelConfig(ctx, configKey+".model")
	if err != nil {
		return nil, err
	}
	apiKey, err := requiredModelConfig(ctx, configKey+".api_key")
	if err != nil {
		return nil, err
	}
	baseURL, err := requiredModelConfig(ctx, configKey+".base_url")
	if err != nil {
		return nil, err
	}

	profile := contextbudget.ConfiguredProfile(ctx, configKey)
	maxCompletionTokens := profile.MaxOutputTokens
	return &openai.ChatModelConfig{
		Model:               modelName,
		APIKey:              apiKey,
		BaseURL:             baseURL,
		MaxCompletionTokens: &maxCompletionTokens,
	}, nil
}

func requiredModelConfig(ctx context.Context, key string) (string, error) {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	if value == nil {
		return "", fmt.Errorf("%s is empty", key)
	}
	configuredValue := strings.TrimSpace(value.String())
	if configuredValue == "" {
		return "", fmt.Errorf("%s is empty", key)
	}
	return configuredValue, nil
}
