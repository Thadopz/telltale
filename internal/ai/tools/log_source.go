package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/authz"
	"github.com/gogf/gf/v2/frame/g"
)

type logCapabilities struct {
	Aggregation bool     `json:"aggregation"`
	Baseline    bool     `json:"baseline"`
	Operation   bool     `json:"operation"`
	Read        bool     `json:"read"`
	Dimensions  []string `json:"dimensions"`
}

type logSource struct {
	URL            string                      `json:"url"`
	ResponseFormat string                      `json:"response_format"`
	Permission     authz.Permission            `json:"permission"`
	Services       map[string]authz.Permission `json:"services"`
	Capabilities   *logCapabilities            `json:"capabilities"`
}

func resolveLogSource(ctx context.Context, id, service string) (string, logSource, error) {
	var sources map[string]logSource
	value, err := g.Cfg().Get(ctx, "tools.logs.sources")
	if err != nil {
		return "", logSource{}, fmt.Errorf("log source configuration unavailable")
	}
	if value == nil || value.Scan(&sources) != nil || len(sources) == 0 {
		return "", logSource{}, fmt.Errorf("no diagnostic log source configured")
	}
	if id == "" {
		if configured, err := g.Cfg().Get(ctx, "tools.logs.default_source"); err == nil && configured != nil {
			id = configured.String()
		}
		if id == "" && len(sources) == 1 {
			for key := range sources {
				id = key
			}
		}
		if id == "" {
			return "", logSource{}, fmt.Errorf("explicit source_id required when multiple log sources have no default")
		}
	}
	source, ok := sources[id]
	if !ok {
		return "", logSource{}, fmt.Errorf("unknown log source")
	}
	permission, allowed := source.Services[service]
	if !allowed || service == "" || permission == "" {
		return "", logSource{}, fmt.Errorf("service outside configured source scope")
	}
	for _, required := range []authz.Permission{authz.PermLogsRead, source.Permission, permission} {
		if required == "" {
			continue
		}
		decision := authz.NewAuthorizer().Authorize(ctx, authz.ToolCall{Name: "diagnostic_log_source", Permission: required, Risk: authz.RiskReadOnly, AgentScope: authz.AgentScopeFromContext(ctx), AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeObservability}})
		if !decision.Allowed {
			return "", logSource{}, fmt.Errorf("log source authorization denied: %s", decision.Reason)
		}
	}
	u, err := url.Parse(source.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", logSource{}, fmt.Errorf("invalid configured log source URL")
	}
	if source.ResponseFormat == "" {
		source.ResponseFormat = "goframe"
	}
	if source.ResponseFormat != "goframe" && source.ResponseFormat != "direct" {
		return "", logSource{}, fmt.Errorf("unsupported diagnostic HTTP response format")
	}
	if source.Capabilities == nil {
		source.Capabilities = &logCapabilities{Aggregation: true, Baseline: true, Operation: true, Read: true, Dimensions: []string{"operation", "level"}}
	}
	for _, dimension := range source.Capabilities.Dimensions {
		if !slices.Contains([]string{"operation", "level"}, dimension) {
			return "", logSource{}, fmt.Errorf("unsupported diagnostic HTTP dimension")
		}
	}
	return id, source, nil
}

func (source logSource) binding() string {
	digest := sha256.Sum256([]byte(source.URL + "\n" + source.ResponseFormat))
	return hex.EncodeToString(digest[:])
}

// Both wire shapes use the diagnostic HTTP operations; neither is a Loki or trace adapter.
func (source logSource) query(ctx context.Context, path string, values url.Values) (map[string]any, error) {
	u, err := url.Parse(source.URL)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("diagnostic log backend unavailable")
	}
	defer response.Body.Close()
	const maxBytes = 2 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes {
		return nil, fmt.Errorf("diagnostic log response exceeds byte budget or cannot be read")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("diagnostic log backend HTTP %d", response.StatusCode)
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return nil, fmt.Errorf("invalid diagnostic log response")
	}
	if source.ResponseFormat == "goframe" {
		if code, ok := data["code"].(float64); !ok || code != 0 {
			return nil, fmt.Errorf("diagnostic log backend rejected query or capability unavailable")
		}
		data, _ = data["data"].(map[string]any)
	}
	if data == nil {
		return nil, fmt.Errorf("missing diagnostic log data")
	}
	return data, nil
}

func (source logSource) locator(sample map[string]any) (string, error) {
	id, ok := sample["id"].(string)
	if !ok || id == "" || len(id) > 256 {
		return "", fmt.Errorf("invalid opaque backend log locator")
	}
	if source.ResponseFormat == "goframe" {
		if value, err := strconv.ParseUint(id, 10, 64); err != nil || value == 0 {
			return "", fmt.Errorf("invalid diagnostic database row ID")
		}
	}
	return id, nil
}
