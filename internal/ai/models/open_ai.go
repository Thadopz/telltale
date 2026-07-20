package models

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/gogf/gf/v2/frame/g"
)

func OpenAIForDeepSeekV31Think(ctx context.Context) (cm model.ToolCallingChatModel, err error) {
	model, err := g.Cfg().Get(ctx, "think_chat_model.model")
	if err != nil {
		return nil, err
	}
	api_key, err := g.Cfg().Get(ctx, "think_chat_model.api_key")
	if err != nil {
		return nil, err
	}
	base_url, err := g.Cfg().Get(ctx, "think_chat_model.base_url")
	if err != nil {
		return nil, err
	}
	config := &openai.ChatModelConfig{
		Model:   model.String(),
		APIKey:  api_key.String(),
		BaseURL: base_url.String(),
	}
	cm, err = openai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func OpenAIForDeepSeekV3Quick(ctx context.Context) (cm model.ToolCallingChatModel, err error) {
	model, err := g.Cfg().Get(ctx, "quick_chat_model.model")
	if err != nil {
		return nil, err
	}
	api_key, err := g.Cfg().Get(ctx, "quick_chat_model.api_key")
	if err != nil {
		return nil, err
	}
	base_url, err := g.Cfg().Get(ctx, "quick_chat_model.base_url")
	if err != nil {
		return nil, err
	}
	config := &openai.ChatModelConfig{
		Model:   model.String(),
		APIKey:  api_key.String(),
		BaseURL: base_url.String(),
	}
	cm, err = openai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func OpenAIForFailover(ctx context.Context) (model.ToolCallingChatModel, error) {
	modelName, err := requiredModelConfig(ctx, "failover_chat_model.model")
	if err != nil {
		return nil, err
	}
	apiKey, err := requiredModelConfig(ctx, "failover_chat_model.api_key")
	if err != nil {
		return nil, err
	}
	baseURL, err := requiredModelConfig(ctx, "failover_chat_model.base_url")
	if err != nil {
		return nil, err
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   modelName,
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create failover chat model: %w", err)
	}
	return chatModel, nil
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
