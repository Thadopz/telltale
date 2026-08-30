package models

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/Thadopz/telltale/internal/authz"
)

type providerCacheTestModel struct {
	generateCalls int
	streamCalls   int
	firstError    error
	receiveError  error
}

func TestProviderCacheDisabledLeavesModelUnwrapped(t *testing.T) {
	inner := &providerCacheTestModel{}
	wrapped, err := wrapProviderCacheModel(context.Background(), inner, "model")
	if err != nil {
		t.Fatalf("wrap disabled provider cache: %v", err)
	}
	if wrapped != inner {
		t.Fatal("disabled provider cache must leave the model unchanged")
	}
}

func (m *providerCacheTestModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.generateCalls++
	if m.generateCalls == 1 && m.firstError != nil {
		return nil, m.firstError
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (m *providerCacheTestModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls++
	if m.streamCalls == 1 && m.firstError != nil {
		return nil, m.firstError
	}
	if m.streamCalls == 1 && m.receiveError != nil {
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(nil, m.receiveError)
		}()
		return reader, nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
}

func TestProviderCacheStreamFailOpenRetriesBeforeFirstChunk(t *testing.T) {
	inner := &providerCacheTestModel{receiveError: errors.New("unsupported parameter prompt_cache_key")}
	wrapper := &providerCacheModel{
		inner:     inner,
		modelName: "model",
		config: providerCacheConfig{
			Enabled: true, KeyField: "prompt_cache_key", KeyPrefix: "telltale", CacheVersion: "1", FailOpen: true,
		},
	}
	reader, err := wrapper.Stream(context.Background(), []*schema.Message{schema.SystemMessage("stable")})
	if err != nil {
		t.Fatalf("start stream: %v", err)
	}
	defer reader.Close()
	message, err := reader.Recv()
	if err != nil || message.Content != "ok" {
		t.Fatalf("expected fallback stream content, message=%#v err=%v", message, err)
	}
	if _, err = reader.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected stream EOF, got %v", err)
	}
	if inner.streamCalls != 2 {
		t.Fatalf("expected exactly one fallback stream call, got %d", inner.streamCalls)
	}
}

func (m *providerCacheTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestProviderCacheKeyChangesWhenIsolationOrStableInputsChange(t *testing.T) {
	config := providerCacheConfig{KeyPrefix: "telltale", CacheVersion: "1"}
	baseContext := authz.WithPrincipal(context.Background(), authz.Principal{TenantID: "tenant-a", Roles: []string{"oncall"}})
	baseMessages := []*schema.Message{schema.SystemMessage("stable prompt"), schema.UserMessage("history")}
	baseKey := buildProviderCacheKey(baseContext, config, "model-a", baseMessages, nil)

	tests := []struct {
		name     string
		ctx      context.Context
		config   providerCacheConfig
		model    string
		messages []*schema.Message
		tools    []*schema.ToolInfo
	}{
		{"tenant", authz.WithPrincipal(context.Background(), authz.Principal{TenantID: "tenant-b", Roles: []string{"oncall"}}), config, "model-a", baseMessages, nil},
		{"roles", authz.WithPrincipal(context.Background(), authz.Principal{TenantID: "tenant-a", Roles: []string{"viewer"}}), config, "model-a", baseMessages, nil},
		{"model", baseContext, config, "model-b", baseMessages, nil},
		{"prompt", baseContext, config, "model-a", []*schema.Message{schema.SystemMessage("changed prompt")}, nil},
		{"tools", baseContext, config, "model-a", baseMessages, []*schema.ToolInfo{{Name: "query_logs"}}},
		{"version", baseContext, providerCacheConfig{KeyPrefix: "telltale", CacheVersion: "2"}, "model-a", baseMessages, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildProviderCacheKey(test.ctx, test.config, test.model, test.messages, test.tools)
			if got == baseKey {
				t.Fatalf("expected %s to change provider cache key", test.name)
			}
		})
	}
}

func TestProviderCacheKeyIgnoresLateDynamicContext(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{TenantID: "tenant-a", Roles: []string{"oncall"}})
	config := providerCacheConfig{KeyPrefix: "telltale", CacheVersion: "1"}
	first := []*schema.Message{schema.SystemMessage("stable"), dynamicCacheTestMessage("Current time: first")}
	second := []*schema.Message{schema.SystemMessage("stable"), dynamicCacheTestMessage("Current time: second")}
	if buildProviderCacheKey(ctx, config, "model", first, nil) != buildProviderCacheKey(ctx, config, "model", second, nil) {
		t.Fatal("late dynamic context must not change the provider routing key")
	}
}

func TestProviderCacheFailOpenRetriesOnlyUnsupportedParameter(t *testing.T) {
	inner := &providerCacheTestModel{firstError: errors.New("unknown parameter prompt_cache_key")}
	wrapper := &providerCacheModel{
		inner:     inner,
		modelName: "model",
		config:    providerCacheConfig{Enabled: true, KeyField: "prompt_cache_key", KeyPrefix: "telltale", CacheVersion: "1", FailOpen: true},
	}
	message, err := wrapper.Generate(context.Background(), []*schema.Message{schema.SystemMessage("stable")})
	if err != nil || message.Content != "ok" || inner.generateCalls != 2 {
		t.Fatalf("expected one fail-open retry, calls=%d message=%#v err=%v", inner.generateCalls, message, err)
	}

	otherFailure := &providerCacheTestModel{firstError: errors.New("authentication failed")}
	wrapper.inner = otherFailure
	_, err = wrapper.Generate(context.Background(), []*schema.Message{schema.SystemMessage("stable")})
	if err == nil || otherFailure.generateCalls != 1 {
		t.Fatalf("non-cache errors must not retry, calls=%d err=%v", otherFailure.generateCalls, err)
	}
}

func TestValidateProviderCacheConfigRejectsCoreFields(t *testing.T) {
	config := providerCacheConfig{
		Enabled:      true,
		KeyField:     "prompt_cache_key",
		KeyPrefix:    "telltale",
		CacheVersion: "1",
		ExtraFields:  map[string]any{"messages": []any{}},
	}
	if err := validateProviderCacheConfig(config); err == nil {
		t.Fatal("expected core request field to be rejected")
	}
	config.ExtraFields = map[string]any{"cache_control": map[string]any{"type": "ephemeral"}}
	if err := validateProviderCacheConfig(config); err != nil {
		t.Fatalf("expected provider-specific cache field to be accepted: %v", err)
	}
}

func dynamicCacheTestMessage(content string) *schema.Message {
	message := schema.SystemMessage(content)
	message.Extra = map[string]any{dynamicContextMessageKey: true}
	return message
}
