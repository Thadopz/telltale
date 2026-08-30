package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/Thadopz/telltale/internal/authz"
)

const (
	defaultProviderCacheKeyField = "prompt_cache_key"
	defaultProviderCachePrefix   = "telltale"
	defaultProviderCacheVersion  = "1"
	maxProviderCacheExtraBytes   = 16 * 1024
	maxProviderCacheExtraDepth   = 8
	dynamicContextMessageKey     = "__telltale_dynamic_context"
)

var safeProviderCacheName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var forbiddenProviderCacheFields = map[string]struct{}{
	"model":                 {},
	"messages":              {},
	"tools":                 {},
	"tool_choice":           {},
	"stream":                {},
	"stream_options":        {},
	"max_tokens":            {},
	"max_completion_tokens": {},
	"user":                  {},
	"authorization":         {},
	"api_key":               {},
	"base_url":              {},
}

type providerCacheConfig struct {
	Enabled      bool
	KeyField     string
	KeyPrefix    string
	CacheVersion string
	ExtraFields  map[string]any
	FailOpen     bool
}

type ProviderCacheTrace struct {
	Enabled        bool
	Fallback       bool
	KeyFingerprint string
}

type providerCacheTraceKey struct{}

func ProviderCacheTraceFromContext(ctx context.Context) ProviderCacheTrace {
	if ctx == nil {
		return ProviderCacheTrace{}
	}
	trace, _ := ctx.Value(providerCacheTraceKey{}).(ProviderCacheTrace)
	return trace
}

type providerCacheModel struct {
	inner     model.ToolCallingChatModel
	modelName string
	config    providerCacheConfig
	tools     []*schema.ToolInfo
}

func wrapProviderCacheModel(ctx context.Context, inner model.ToolCallingChatModel, modelName string) (model.ToolCallingChatModel, error) {
	config, err := loadProviderCacheConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return inner, nil
	}
	return &providerCacheModel{inner: inner, modelName: modelName, config: config}, nil
}

func (m *providerCacheModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &providerCacheModel{
		inner:     bound,
		modelName: m.modelName,
		config:    m.config,
		tools:     cloneToolInfos(tools),
	}, nil
}

func (m *providerCacheModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	cacheFields, cacheTrace := m.requestCacheFields(ctx, input, opts)
	cacheContext := context.WithValue(ctx, providerCacheTraceKey{}, cacheTrace)
	cacheOptions := appendProviderCacheOption(opts, cacheFields)

	message, err := m.inner.Generate(cacheContext, input, cacheOptions...)
	if err == nil || !m.config.FailOpen || !isUnsupportedCacheParameterError(err) {
		return message, err
	}

	g.Log().Warningf(ctx, "provider cache fields are unsupported; retrying without cache hint model=%s fingerprint=%s err=%v", m.modelName, cacheTrace.KeyFingerprint, err)
	fallbackTrace := cacheTrace
	fallbackTrace.Fallback = true
	fallbackContext := context.WithValue(ctx, providerCacheTraceKey{}, fallbackTrace)
	return m.inner.Generate(fallbackContext, input, opts...)
}

func (m *providerCacheModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	cacheFields, cacheTrace := m.requestCacheFields(ctx, input, opts)
	cacheContext := context.WithValue(ctx, providerCacheTraceKey{}, cacheTrace)
	cacheOptions := appendProviderCacheOption(opts, cacheFields)

	reader, err := m.inner.Stream(cacheContext, input, cacheOptions...)
	if err == nil {
		if !m.config.FailOpen {
			return reader, nil
		}
		return m.streamWithFailOpen(ctx, input, opts, reader, cacheTrace), nil
	}
	if !m.config.FailOpen || !isUnsupportedCacheParameterError(err) {
		return reader, err
	}

	// Stream only retries when the initial request fails. Once a reader exists,
	// a later Recv error is returned as-is so emitted content is never repeated.
	g.Log().Warningf(ctx, "provider cache fields are unsupported; retrying stream without cache hint model=%s fingerprint=%s err=%v", m.modelName, cacheTrace.KeyFingerprint, err)
	fallbackTrace := cacheTrace
	fallbackTrace.Fallback = true
	fallbackContext := context.WithValue(ctx, providerCacheTraceKey{}, fallbackTrace)
	return m.inner.Stream(fallbackContext, input, opts...)
}

func (m *providerCacheModel) streamWithFailOpen(ctx context.Context, input []*schema.Message, opts []model.Option, initial *schema.StreamReader[*schema.Message], cacheTrace ProviderCacheTrace) *schema.StreamReader[*schema.Message] {
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		current := initial
		retried := false
		emitted := false
		for {
			chunk, receiveErr := current.Recv()
			if errors.Is(receiveErr, io.EOF) {
				current.Close()
				return
			}
			if receiveErr != nil {
				current.Close()
				if !emitted && !retried && isUnsupportedCacheParameterError(receiveErr) {
					retried = true
					fallbackTrace := cacheTrace
					fallbackTrace.Fallback = true
					fallbackContext := context.WithValue(ctx, providerCacheTraceKey{}, fallbackTrace)
					g.Log().Warningf(ctx, "provider cache fields are unsupported before first stream chunk; retrying without cache hint model=%s fingerprint=%s err=%v", m.modelName, cacheTrace.KeyFingerprint, receiveErr)
					fallbackReader, fallbackErr := m.inner.Stream(fallbackContext, input, opts...)
					if fallbackErr == nil {
						current = fallbackReader
						continue
					}
					receiveErr = fallbackErr
				}
				writer.Send(nil, receiveErr)
				return
			}
			emitted = true
			if writer.Send(chunk, nil) {
				current.Close()
				return
			}
		}
	}()
	return reader
}

func (m *providerCacheModel) requestCacheFields(ctx context.Context, messages []*schema.Message, opts []model.Option) (map[string]any, ProviderCacheTrace) {
	tools := cloneToolInfos(m.tools)
	effectiveModelName := m.modelName
	commonOptions := model.GetCommonOptions(nil, opts...)
	if commonOptions != nil {
		if commonOptions.Model != nil && strings.TrimSpace(*commonOptions.Model) != "" {
			effectiveModelName = strings.TrimSpace(*commonOptions.Model)
		}
		tools = append(tools, commonOptions.Tools...)
		tools = append(tools, commonOptions.DeferredTools...)
		if commonOptions.ToolSearchTool != nil {
			tools = append(tools, commonOptions.ToolSearchTool)
		}
	}
	cacheKey := buildProviderCacheKey(ctx, m.config, effectiveModelName, messages, tools)
	fields := cloneAnyMap(m.config.ExtraFields)
	fields[m.config.KeyField] = cacheKey
	fingerprint := ""
	if separator := strings.LastIndex(cacheKey, "-"); separator >= 0 && separator+1 < len(cacheKey) {
		fingerprint = cacheKey[separator+1:]
	}
	if len(fingerprint) > 8 {
		fingerprint = fingerprint[:8]
	}
	return fields, ProviderCacheTrace{Enabled: true, KeyFingerprint: fingerprint}
}

func appendProviderCacheOption(opts []model.Option, fields map[string]any) []model.Option {
	result := make([]model.Option, 0, len(opts)+1)
	result = append(result, opts...)
	result = append(result, openaimodel.WithExtraFields(fields))
	return result
}

func loadProviderCacheConfig(ctx context.Context) (providerCacheConfig, error) {
	config := providerCacheConfig{
		KeyField:     defaultProviderCacheKeyField,
		KeyPrefix:    defaultProviderCachePrefix,
		CacheVersion: defaultProviderCacheVersion,
		ExtraFields:  map[string]any{},
		FailOpen:     true,
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.enabled"); err == nil && value != nil {
		config.Enabled = value.Bool()
	}
	if !config.Enabled {
		return config, nil
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.key_field"); err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		config.KeyField = strings.TrimSpace(value.String())
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.key_prefix"); err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		config.KeyPrefix = strings.TrimSpace(value.String())
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.cache_version"); err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		config.CacheVersion = strings.TrimSpace(value.String())
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.fail_open"); err == nil && value != nil {
		config.FailOpen = value.Bool()
	}
	if value, err := g.Cfg().Get(ctx, "provider_cache.extra_fields"); err == nil && value != nil {
		extraFields, parseErr := normalizeProviderCacheExtraFields(value.Map())
		if parseErr != nil {
			return providerCacheConfig{}, parseErr
		}
		config.ExtraFields = extraFields
	}
	if err := validateProviderCacheConfig(config); err != nil {
		return providerCacheConfig{}, err
	}
	return config, nil
}

func validateProviderCacheConfig(config providerCacheConfig) error {
	if !safeProviderCacheName.MatchString(config.KeyField) || len(config.KeyField) > 64 {
		return fmt.Errorf("provider_cache.key_field must contain only letters, numbers, dot, underscore, or dash and be at most 64 characters")
	}
	if isForbiddenProviderCacheField(config.KeyField) {
		return fmt.Errorf("provider_cache.key_field %q cannot override a core model request field", config.KeyField)
	}
	if !safeProviderCacheName.MatchString(config.KeyPrefix) || len(config.KeyPrefix) > 32 {
		return fmt.Errorf("provider_cache.key_prefix must contain only letters, numbers, dot, underscore, or dash and be at most 32 characters")
	}
	if !safeProviderCacheName.MatchString(config.CacheVersion) || len(config.CacheVersion) > 32 {
		return fmt.Errorf("provider_cache.cache_version must contain only letters, numbers, dot, underscore, or dash and be at most 32 characters")
	}
	for key := range config.ExtraFields {
		if isForbiddenProviderCacheField(key) {
			return fmt.Errorf("provider_cache.extra_fields cannot override core model request field %q", key)
		}
	}
	return nil
}

func normalizeProviderCacheExtraFields(raw map[string]any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode provider_cache.extra_fields: %w", err)
	}
	if len(encoded) > maxProviderCacheExtraBytes {
		return nil, fmt.Errorf("provider_cache.extra_fields exceeds %d bytes", maxProviderCacheExtraBytes)
	}
	var normalized map[string]any
	if err = json.Unmarshal(encoded, &normalized); err != nil {
		return nil, fmt.Errorf("decode provider_cache.extra_fields: %w", err)
	}
	if providerCacheValueDepth(normalized) > maxProviderCacheExtraDepth {
		return nil, fmt.Errorf("provider_cache.extra_fields exceeds maximum nesting depth %d", maxProviderCacheExtraDepth)
	}
	return normalized, nil
}

func providerCacheValueDepth(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		maxDepth := 1
		for _, child := range typed {
			depth := 1 + providerCacheValueDepth(child)
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		return maxDepth
	case []any:
		maxDepth := 1
		for _, child := range typed {
			depth := 1 + providerCacheValueDepth(child)
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		return maxDepth
	default:
		return 0
	}
}

func buildProviderCacheKey(ctx context.Context, config providerCacheConfig, modelName string, messages []*schema.Message, tools []*schema.ToolInfo) string {
	principal := authz.PrincipalFromContext(ctx)
	roles := append([]string(nil), principal.Roles...)
	sort.Strings(roles)
	material := struct {
		Tenant       string             `json:"tenant"`
		Roles        []string           `json:"roles"`
		Model        string             `json:"model"`
		Version      string             `json:"version"`
		StablePrompt []string           `json:"stable_prompt"`
		Tools        []*schema.ToolInfo `json:"tools"`
	}{
		Tenant:       strings.TrimSpace(principal.TenantID),
		Roles:        roles,
		Model:        strings.TrimSpace(modelName),
		Version:      config.CacheVersion,
		StablePrompt: stableSystemPrefix(messages),
		Tools:        tools,
	}
	encoded, _ := json.Marshal(material)
	digest := sha256.Sum256(encoded)
	return config.KeyPrefix + "-v" + config.CacheVersion + "-" + hex.EncodeToString(digest[:])[:32]
}

func stableSystemPrefix(messages []*schema.Message) []string {
	stable := make([]string, 0, 2)
	for _, message := range messages {
		if message == nil {
			continue
		}
		if message.Extra != nil {
			if dynamic, _ := message.Extra[dynamicContextMessageKey].(bool); dynamic {
				break
			}
		}
		if message.Role != schema.System {
			break
		}
		stable = append(stable, message.Content)
	}
	return stable
}

func isUnsupportedCacheParameterError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	patterns := []string{
		"unknown parameter",
		"unsupported parameter",
		"unrecognized request argument",
		"unrecognized field",
		"extra inputs are not permitted",
		"unexpected field",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isForbiddenProviderCacheField(field string) bool {
	_, forbidden := forbiddenProviderCacheFields[strings.ToLower(strings.TrimSpace(field))]
	return forbidden
}

func cloneAnyMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source)+1)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneToolInfos(tools []*schema.ToolInfo) []*schema.ToolInfo {
	cloned := make([]*schema.ToolInfo, len(tools))
	copy(cloned, tools)
	return cloned
}
