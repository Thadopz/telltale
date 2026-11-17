package tools

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/mem"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/gogf/gf/v2/frame/g"
)

var telemetryMetricPattern = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:]*$`)

// Sources are explicit security boundaries; backend tenant headers never come from model input.
type telemetrySource struct {
	Kind             string                      `json:"kind"`
	URL              string                      `json:"url"`
	Tenant           string                      `json:"tenant"`
	BackendTenant    string                      `json:"backend_tenant"`
	Environment      string                      `json:"environment"`
	Services         map[string]authz.Permission `json:"services"`
	Permission       authz.Permission            `json:"permission"`
	BearerEnv        string                      `json:"bearer_env"`
	ServiceLabel     string                      `json:"service_label"`
	InstanceLabel    string                      `json:"instance_label"`
	EnvironmentLabel string                      `json:"environment_label"`
}

type TelemetryQuery struct {
	Source        string            `json:"source"`
	Environment   string            `json:"environment"`
	Service       string            `json:"service"`
	Instance      string            `json:"instance,omitempty"`
	Start         string            `json:"start"`
	End           string            `json:"end"`
	Metric        string            `json:"metric,omitempty" jsonschema:"description=Exact metric name; range evaluation does not aggregate values"`
	StepSeconds   int               `json:"step_seconds,omitempty"`
	Labels        map[string]string `json:"labels,omitempty" jsonschema:"description=Exact indexed labels for metrics or Loki; no inferred JSON field parsing"`
	Keyword       string            `json:"keyword,omitempty" jsonschema:"description=Loki case-sensitive literal line substring"`
	Operation     string            `json:"operation,omitempty"`
	Status        string            `json:"status,omitempty" jsonschema:"description=Tempo error or ok"`
	MinDurationMS int               `json:"min_duration_ms,omitempty"`
	MaxDurationMS int               `json:"max_duration_ms,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	Cursor        string            `json:"cursor,omitempty"`
}

type telemetryLocator struct {
	Query    TelemetryQuery `json:"query"`
	Kind     string         `json:"kind"`
	Binding  string         `json:"binding"`
	User     string         `json:"user"`
	Tenant   string         `json:"tenant"`
	Session  string         `json:"session"`
	Expires  int64          `json:"expires"`
	TraceID  string         `json:"trace_id,omitempty"`
	Offset   int            `json:"offset,omitempty"`
	Snapshot string         `json:"snapshot,omitempty"`
}

func telemetrySources(ctx context.Context) (map[string]telemetrySource, error) {
	v, err := g.Cfg().Get(ctx, "tools.telemetry.sources")
	if err != nil {
		return nil, fmt.Errorf("telemetry configuration unavailable")
	}
	sources := map[string]telemetrySource{}
	if v != nil && v.Scan(&sources) != nil {
		return nil, fmt.Errorf("invalid telemetry configuration")
	}
	return sources, nil
}

func resolveTelemetry(ctx context.Context, q TelemetryQuery, kind string) (telemetrySource, error) {
	sources, err := telemetrySources(ctx)
	if err != nil {
		return telemetrySource{}, err
	}
	s, ok := sources[q.Source]
	p := authz.PrincipalFromContext(ctx)
	permission := s.Services[q.Service]
	if !ok || s.Kind != kind || s.Tenant == "" || s.Tenant != p.TenantID || q.Environment != s.Environment || permission == "" || p.UserID == "" {
		return s, fmt.Errorf("source or requested scope unavailable to principal")
	}
	basePermission := authz.PermLogsRead
	if kind == "prometheus" {
		basePermission = authz.PermMetricsRead
	}
	for _, required := range []authz.Permission{basePermission, s.Permission, permission} {
		if required == "" {
			continue
		}
		d := authz.NewAuthorizer().Authorize(ctx, authz.ToolCall{
			Name: "telemetry_source", Permission: required, Risk: authz.RiskReadOnly,
			AgentScope:         authz.AgentScopeFromContext(ctx),
			AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeObservability},
		})
		if !d.Allowed {
			return s, fmt.Errorf("telemetry scope authorization denied")
		}
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return s, fmt.Errorf("invalid telemetry source URL")
	}
	for _, label := range []string{s.ServiceLabel, s.InstanceLabel, s.EnvironmentLabel} {
		if label != "" && !prometheusLabelPattern.MatchString(label) {
			return s, fmt.Errorf("invalid telemetry scope label")
		}
	}
	if kind != "tempo" && (s.ServiceLabel == "" || s.EnvironmentLabel == "" || (q.Instance != "" && s.InstanceLabel == "")) {
		return s, fmt.Errorf("scope label mapping missing")
	}
	return s, nil
}

func telemetryRange(q *TelemetryQuery) (time.Time, time.Time, error) {
	start, e1 := time.Parse(time.RFC3339Nano, q.Start)
	end, e2 := time.Parse(time.RFC3339Nano, q.End)
	if e1 != nil || e2 != nil || !end.After(start) || end.Sub(start) > 24*time.Hour {
		return start, end, fmt.Errorf("explicit RFC3339 start/end required, maximum 24 hours")
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.Keyword) > 256 || len(q.Operation) > 256 {
		return start, end, fmt.Errorf("invalid query limit or filter size")
	}
	return start, end, nil
}

func telemetryAEAD() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("TELLTALE_TELEMETRY_KEY"))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("TELLTALE_TELEMETRY_KEY must contain a base64 encoded 32-byte shared secret")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func telemetryBinding(s telemetrySource) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sealTelemetry(ctx context.Context, s telemetrySource, loc telemetryLocator) (string, error) {
	aead, err := telemetryAEAD()
	if err != nil {
		return "", err
	}
	p := authz.PrincipalFromContext(ctx)
	loc.User, loc.Tenant, loc.Session = p.UserID, p.TenantID, contextString(ctx, "client_id")
	loc.Binding, loc.Expires = telemetryBinding(s), time.Now().Add(10*time.Minute).Unix()
	b, err := json.Marshal(loc)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, b, []byte("telltale-telemetry-v1"))), nil
}

func openTelemetry(ctx context.Context, ref string) (telemetryLocator, telemetrySource, error) {
	var loc telemetryLocator
	aead, err := telemetryAEAD()
	if err != nil {
		return loc, telemetrySource{}, err
	}
	b, err := base64.RawURLEncoding.DecodeString(ref)
	if err != nil || len(b) < aead.NonceSize() || len(b) > 16384 {
		return loc, telemetrySource{}, fmt.Errorf("invalid evidence reference")
	}
	b, err = aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], []byte("telltale-telemetry-v1"))
	if err != nil || json.Unmarshal(b, &loc) != nil {
		return loc, telemetrySource{}, fmt.Errorf("invalid evidence authentication")
	}
	p := authz.PrincipalFromContext(ctx)
	if loc.Expires <= time.Now().Unix() || loc.User != p.UserID || loc.Tenant != p.TenantID || loc.Session != contextString(ctx, "client_id") {
		return loc, telemetrySource{}, fmt.Errorf("expired evidence or principal/session mismatch")
	}
	s, err := resolveTelemetry(ctx, loc.Query, loc.Kind)
	if err == nil && loc.Binding != telemetryBinding(s) {
		err = fmt.Errorf("evidence source configuration changed")
	}
	return loc, s, err
}

func (s telemetrySource) get(ctx context.Context, path string, values url.Values, out any) error {
	return s.getLimited(ctx, path, values, out, 2*1024*1024)
}

func (s telemetrySource) getLimited(ctx context.Context, path string, values url.Values, out any, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.URL, "/")+path+"?"+values.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if s.BackendTenant != "" {
		req.Header.Set("X-Scope-OrgID", s.BackendTenant)
	}
	if s.BearerEnv != "" {
		secret := os.Getenv(s.BearerEnv)
		if secret == "" {
			return fmt.Errorf("configured backend credential unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telemetry backend unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry backend HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil || int64(len(b)) > maxBytes {
		return fmt.Errorf("telemetry response exceeds %d bytes or read failed; evidence unavailable", maxBytes)
	}
	if json.Unmarshal(b, out) != nil {
		return fmt.Errorf("invalid telemetry backend JSON")
	}
	return nil
}

func telemetrySelector(s telemetrySource, q TelemetryQuery) (string, error) {
	if len(q.Labels) > 64 {
		return "", fmt.Errorf("at most 64 indexed label filters allowed")
	}
	filter := prometheusAlertFilter{matchLabels: map[string]string{}}
	for key, value := range q.Labels {
		validated, err := normalizePrometheusAlertFilter(&QueryPrometheusAlertsInput{MatchLabels: map[string]string{key: value}})
		if err != nil {
			return "", err
		}
		for name, exact := range validated.matchLabels {
			filter.matchLabels[name] = exact
		}
	}
	for key, value := range map[string]string{s.ServiceLabel: q.Service, s.EnvironmentLabel: q.Environment, s.InstanceLabel: q.Instance} {
		if key == "" || value == "" {
			continue
		}
		if old, ok := filter.matchLabels[key]; ok && old != value {
			return "", fmt.Errorf("label conflicts with authorized scope")
		}
		filter.matchLabels[key] = value
	}
	keys := make([]string, 0, len(filter.matchLabels))
	for key := range filter.matchLabels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Quote(filter.matchLabels[key]))
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func NewDiscoverTelemetryTool() (tool.InvokableTool, error) {
	return utils.InferTool("discover_telemetry", "List configured authorized telemetry source/service scopes; configuration does not prove backend readiness. Legacy diagnostic logs remain available through analyze_log_window.", func(ctx context.Context, _ *struct{}) (string, error) {
		sources, err := telemetrySources(ctx)
		if err != nil {
			return marshalToolError(err, "discovery failed"), nil
		}
		rows := []any{}
		for id, s := range sources {
			for service := range s.Services {
				q := TelemetryQuery{Source: id, Environment: s.Environment, Service: service}
				if _, err := resolveTelemetry(ctx, q, s.Kind); err != nil {
					continue
				}
				if !slices.Contains([]string{"prometheus", "loki", "tempo"}, s.Kind) {
					continue
				}
				rows = append(rows, map[string]any{"source": id, "kind": s.Kind, "service": service, "environment": s.Environment, "readiness": "not_probed"})
			}
		}
		var legacy map[string]logSource
		if value, err := g.Cfg().Get(ctx, "tools.logs.sources"); err == nil && value != nil {
			if err := value.Scan(&legacy); err != nil {
				return marshalToolError(err, "diagnostic log discovery configuration invalid"), nil
			}
			for id, source := range legacy {
				for service := range source.Services {
					_, resolved, err := resolveLogSource(ctx, id, service)
					if err == nil {
						rows = append(rows, map[string]any{"source": id, "kind": "diagnostic_http", "service": service, "capabilities": resolved.Capabilities, "tools": []string{"analyze_log_window", "read_log_evidence"}, "readiness": "not_probed"})
					}
				}
			}
		}
		return marshalToolData(rows, "Configured authorized scopes; empty means no accessible configured source."), nil
	})
}

func NewQueryMetricsTool() (tool.InvokableTool, error) {
	return utils.InferTool("query_metrics", "Query an exact scoped Prometheus metric over an explicit range. Step evaluations may miss spikes; repeat with smaller step or baseline timestamps. Maximum 2000 total points and 100 series.", func(ctx context.Context, q *TelemetryQuery) (string, error) {
		out, err := queryTelemetryMetrics(ctx, *q)
		if err != nil {
			return marshalToolError(err, "metric evidence unavailable"), nil
		}
		return marshalTelemetryData(ctx, out, "Prometheus range evaluation; no implicit averaging."), nil
	})
}

func queryTelemetryMetrics(ctx context.Context, q TelemetryQuery) (any, error) {
	start, end, err := telemetryRange(&q)
	if err != nil {
		return nil, err
	}
	s, err := resolveTelemetry(ctx, q, "prometheus")
	if err != nil {
		return nil, err
	}
	if !telemetryMetricPattern.MatchString(q.Metric) || q.StepSeconds < 1 || int(end.Sub(start)/time.Second)/q.StepSeconds+1 > 2000/q.Limit {
		return nil, fmt.Errorf("invalid metric/step or requested point budget exceeds 2000; choose explicit step or fewer series")
	}
	selector, err := telemetrySelector(s, q)
	if err != nil {
		return nil, err
	}
	query := q.Metric + selector
	canonical, _ := json.Marshal(q)
	if !contextbudget.BeginLogCheck(ctx, "query_metrics\n"+string(canonical)) {
		return nil, fmt.Errorf("duplicate query or investigation call budget exhausted")
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []json.RawMessage `json:"result"`
		} `json:"data"`
		Warnings []string `json:"warnings"`
	}
	err = s.get(ctx, "/api/v1/query_range", url.Values{"query": {query}, "start": {start.Format(time.RFC3339Nano)}, "end": {end.Format(time.RFC3339Nano)}, "step": {strconv.Itoa(q.StepSeconds)}, "limit": {strconv.Itoa(q.Limit + 1)}}, &result)
	if err != nil {
		return nil, err
	}
	if result.Status != "success" || result.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("Prometheus query failed or unsupported result type")
	}
	truncated := len(result.Data.Result) > q.Limit
	result.Data.Result = result.Data.Result[:min(q.Limit, len(result.Data.Result))]
	points := 0
	for _, series := range result.Data.Result {
		var matrix struct {
			Values     []json.RawMessage `json:"values"`
			Histograms []json.RawMessage `json:"histograms"`
		}
		if json.Unmarshal(series, &matrix) != nil {
			return nil, fmt.Errorf("invalid Prometheus series")
		}
		points += len(matrix.Values) + len(matrix.Histograms)
		if points > 2000 {
			return nil, fmt.Errorf("Prometheus returned more than 2000 points; narrow query")
		}
	}
	return map[string]any{"telemetry": map[string]any{
		"scope": q, "backend": "prometheus", "query": query, "query_status": "success",
		"representation": "step_evaluations_not_raw_scrapes", "spikes_may_be_missed": true,
		"matched": "unknown", "returned": len(result.Data.Result), "truncated": truncated,
		"reason": "series limit; narrow labels if truncated", "warnings": result.Warnings,
		"point_budget": 2000, "returned_points": points, "backend_byte_budget": 2 * 1024 * 1024,
	}, "records": result.Data.Result}, nil
}

type telemetryLog struct {
	Timestamp string            `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
	Line      string            `json:"line"`
}

func NewSearchLogsTool() (tool.InvokableTool, error) {
	return utils.InferTool("search_logs", "Search authorized Loki indexed labels and literal line substring. Stable bounded snapshot pagination; changed snapshots fail explicitly. Maximum 5000 matches per range; narrow saturated ranges. Cursor must be supplied with identical query.", func(ctx context.Context, q *TelemetryQuery) (string, error) {
		out, err := searchTelemetryLogs(ctx, *q)
		if err != nil {
			return marshalToolError(err, "log evidence unavailable"), nil
		}
		return marshalTelemetryData(ctx, out, "Loki search; indexed label equality and case-sensitive substring semantics."), nil
	})
}

func searchTelemetryLogs(ctx context.Context, q TelemetryQuery, raw ...bool) (any, error) {
	start, end, err := telemetryRange(&q)
	if err != nil {
		return nil, err
	}
	s, err := resolveTelemetry(ctx, q, "loki")
	if err != nil {
		return nil, err
	}
	if _, err := telemetryAEAD(); err != nil {
		return nil, err
	}
	canonical := q
	canonical.Cursor = ""
	encoded, _ := json.Marshal(canonical)
	offset := 0
	snapshot := ""
	if q.Cursor != "" {
		loc, _, err := openTelemetry(ctx, q.Cursor)
		if err != nil {
			return nil, err
		}
		if q.Limit > loc.Query.Limit {
			return nil, fmt.Errorf("cursor page limit may only decrease")
		}
		loc.Query.Limit = q.Limit
		previous, _ := json.Marshal(loc.Query)
		if loc.Kind != "loki" || loc.Snapshot == "" || string(previous) != string(encoded) {
			return nil, fmt.Errorf("cursor query mismatch")
		}
		offset, snapshot = loc.Offset, loc.Snapshot
	}
	check, _ := json.Marshal(q)
	if !contextbudget.BeginLogCheck(ctx, "search_logs\n"+string(check)) {
		return nil, fmt.Errorf("duplicate page or investigation call budget exhausted")
	}
	selector, err := telemetrySelector(s, q)
	if err != nil {
		return nil, err
	}
	query := selector
	if q.Keyword != "" {
		query += " |= " + strconv.Quote(q.Keyword)
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string   `json:"stream"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	err = s.get(ctx, "/loki/api/v1/query_range", url.Values{"query": {query}, "start": {strconv.FormatInt(start.UnixNano(), 10)}, "end": {strconv.FormatInt(end.UnixNano(), 10)}, "direction": {"forward"}, "limit": {"5000"}}, &result)
	if err != nil {
		return nil, err
	}
	if result.Status != "success" || result.Data.ResultType != "streams" {
		return nil, fmt.Errorf("Loki query failed or unsupported result")
	}
	rows := []telemetryLog{}
	for _, stream := range result.Data.Result {
		for _, value := range stream.Values {
			var timestamp, line string
			if len(value) < 2 || json.Unmarshal(value[0], &timestamp) != nil || json.Unmarshal(value[1], &line) != nil {
				return nil, fmt.Errorf("invalid Loki log entry")
			}
			if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
				return nil, fmt.Errorf("invalid Loki timestamp")
			}
			rows = append(rows, telemetryLog{Timestamp: timestamp, Labels: stream.Stream, Line: line})
		}
	}
	slices.SortFunc(rows, func(a, b telemetryLog) int {
		if a.Timestamp != b.Timestamp {
			x, _ := strconv.ParseInt(a.Timestamp, 10, 64)
			y, _ := strconv.ParseInt(b.Timestamp, 10, 64)
			if x < y {
				return -1
			}
			return 1
		}
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return strings.Compare(string(x), string(y))
	})
	b, _ := json.Marshal(rows)
	h := sha256.Sum256(b)
	digest := hex.EncodeToString(h[:])
	if snapshot != "" && snapshot != digest {
		return nil, fmt.Errorf("Loki snapshot changed; restart query to avoid missing or duplicate entries")
	}
	if offset < 0 || offset > len(rows) {
		return nil, fmt.Errorf("invalid snapshot offset")
	}
	next := min(len(rows), offset+q.Limit)
	cursor := ""
	if next < len(rows) {
		cursor, err = sealTelemetry(ctx, s, telemetryLocator{Query: canonical, Kind: "loki", Offset: next, Snapshot: digest})
		if err != nil {
			return nil, err
		}
	}
	retryCursor, err := sealTelemetry(ctx, s, telemetryLocator{Query: canonical, Kind: "loki", Offset: offset, Snapshot: digest})
	if err != nil {
		return nil, err
	}
	records := []any{}
	for _, row := range rows[offset:next] {
		readQuery := canonical
		ts, _ := strconv.ParseInt(row.Timestamp, 10, 64)
		readQuery.Start = time.Unix(0, ts).Format(time.RFC3339Nano)
		readQuery.End = time.Unix(0, ts+1).Format(time.RFC3339Nano)
		readQuery.Labels, readQuery.Keyword = row.Labels, ""
		ref, err := sealTelemetry(ctx, s, telemetryLocator{Query: readQuery, Kind: "loki"})
		if err != nil {
			return nil, err
		}
		line := boundedText(row.Line, 256)
		if len(raw) > 0 && raw[0] {
			line = row.Line
		}
		records = append(records, map[string]any{"timestamp": row.Timestamp, "labels": row.Labels, "snippet": line, "evidence_ref": ref})
	}
	matched := any(len(rows))
	if len(rows) >= 5000 {
		matched = "unknown"
	}
	return map[string]any{"telemetry": map[string]any{
		"scope": canonical, "backend": "loki", "query": query, "query_status": "success", "representation": "raw_log_snippets",
		"matched": matched, "returned": len(records), "truncated": next < len(rows) || len(rows) >= 5000,
		"reason":      "5000-entry snapshot cap; saturated ranges require narrower timestamps; late ingestion invalidates cursor",
		"next_cursor": cursor, "retry_cursor": retryCursor, "line_budget": q.Limit, "backend_byte_budget": 2 * 1024 * 1024,
	}, "records": records}, nil
}

// Only authenticated locators bypass text redaction; backend text always follows normal redaction.
func redactTelemetryData(ctx context.Context, original any) any {
	b, _ := json.Marshal(original)
	var data any
	if json.Unmarshal(b, &data) != nil {
		return mem.RedactToolPayload(original)
	}
	redacted := mem.RedactToolPayload(data)
	if json.Unmarshal(b, &data) != nil {
		return redacted
	}
	var restore func(any, any)
	restore = func(before, after any) {
		switch before := before.(type) {
		case map[string]any:
			after, ok := after.(map[string]any)
			if !ok {
				return
			}
			for key, value := range before {
				if slices.Contains([]string{"evidence_ref", "next_cursor", "retry_cursor"}, key) {
					if ref, ok := value.(string); ok && ref != "" {
						if _, _, err := openTelemetry(ctx, ref); err == nil {
							after[key] = ref
						}
					}
					continue
				}
				restore(value, after[key])
			}
		case []any:
			after, ok := after.([]any)
			if !ok {
				return
			}
			for i := range min(len(before), len(after)) {
				restore(before[i], after[i])
			}
		}
	}
	restore(data, redacted)
	return redacted
}

func NewSearchTracesTool() (tool.InvokableTool, error) {
	return utils.InferTool("search_traces", "Search scoped Tempo traces using typed service, instance, operation, status and duration filters. Sampling and missing spans unknown; searches may be incomplete. Query normal status for controls.", func(ctx context.Context, q *TelemetryQuery) (string, error) {
		out, err := searchTelemetryTraces(ctx, *q)
		if err != nil {
			return marshalToolError(err, "trace evidence unavailable"), nil
		}
		return marshalTelemetryData(ctx, out, "Tempo trace search; absence cannot establish health."), nil
	})
}

func searchTelemetryTraces(ctx context.Context, q TelemetryQuery) (any, error) {
	start, end, err := telemetryRange(&q)
	if err != nil {
		return nil, err
	}
	s, err := resolveTelemetry(ctx, q, "tempo")
	if err != nil {
		return nil, err
	}
	if _, err := telemetryAEAD(); err != nil {
		return nil, err
	}
	if q.Cursor != "" || q.MinDurationMS < 0 || q.MaxDurationMS < 0 || (q.MaxDurationMS > 0 && q.MaxDurationMS < q.MinDurationMS) {
		return nil, fmt.Errorf("invalid duration bounds; Tempo search cursor unsupported")
	}
	conditions := []string{"resource.service.name = " + strconv.Quote(q.Service), "resource.deployment.environment = " + strconv.Quote(q.Environment)}
	if q.Instance != "" {
		conditions = append(conditions, "resource.service.instance.id = "+strconv.Quote(q.Instance))
	}
	if q.Operation != "" {
		conditions = append(conditions, "name = "+strconv.Quote(q.Operation))
	}
	if q.Status != "" {
		if q.Status != "ok" && q.Status != "error" {
			return nil, fmt.Errorf("status must be ok or error")
		}
		conditions = append(conditions, "status = "+q.Status)
	}
	if q.MinDurationMS > 0 {
		conditions = append(conditions, "duration >= "+strconv.Itoa(q.MinDurationMS)+"ms")
	}
	if q.MaxDurationMS > 0 {
		conditions = append(conditions, "duration <= "+strconv.Itoa(q.MaxDurationMS)+"ms")
	}
	query := "{" + strings.Join(conditions, " && ") + "}"
	canonical, _ := json.Marshal(q)
	if !contextbudget.BeginLogCheck(ctx, "search_traces\n"+string(canonical)) {
		return nil, fmt.Errorf("duplicate query or investigation call budget exhausted")
	}
	var result struct {
		Traces  []map[string]any `json:"traces"`
		Metrics map[string]any   `json:"metrics"`
	}
	err = s.get(ctx, "/api/search", url.Values{"q": {query}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix()+1, 10)}, "limit": {strconv.Itoa(q.Limit)}}, &result)
	if err != nil {
		return nil, err
	}
	if result.Metrics == nil {
		return nil, fmt.Errorf("Tempo search response missing completeness metrics")
	}
	if len(result.Traces) > q.Limit {
		result.Traces = result.Traces[:q.Limit]
	}
	for _, trace := range result.Traces {
		id, _ := trace["traceID"].(string)
		if _, err := hex.DecodeString(id); err != nil || len(id) != 32 {
			return nil, fmt.Errorf("invalid Tempo trace ID")
		}
		ref, err := sealTelemetry(ctx, s, telemetryLocator{Query: q, Kind: "tempo", TraceID: id})
		if err != nil {
			return nil, err
		}
		trace["evidence_ref"] = ref
	}
	return map[string]any{"telemetry": map[string]any{
		"scope": q, "backend": "tempo", "query": query, "query_status": "success", "representation": "trace_search_metadata",
		"actual_start": start.Unix(), "actual_end": end.Unix() + 1, "matched": "unknown", "returned": len(result.Traces),
		"truncated": len(result.Traces) >= q.Limit, "sampling": "unknown", "missing_spans": "unknown",
		"reason": "Tempo search limit and ingestion sampling; narrow range for additional searches", "backend_metrics": result.Metrics,
	}, "records": result.Traces}, nil
}

type ReadTelemetryInput struct {
	Reference      string `json:"reference"`
	ContextSeconds int    `json:"context_seconds,omitempty" jsonschema:"description=Loki context before and after entry, maximum 60 seconds; zero reads timestamp only"`
	PageRunes      int    `json:"page_runes,omitempty" jsonschema:"description=Tempo JSON fragment size in Unicode characters, default 512, range 64 to 2048; continue using next_cursor as reference"`
}

func NewReadEvidenceTool() (tool.InvokableTool, error) {
	return utils.InferTool("read_evidence", "Read authenticated telemetry locator. Loki supports timestamp/stream context. Tempo requires source-wide permission and returns paged redacted JSON fragments; pass next_cursor as reference and reduce page_runes after output truncation. Missing spans remain unknown.", func(ctx context.Context, input *ReadTelemetryInput) (string, error) {
		loc, s, err := openTelemetry(ctx, input.Reference)
		if err != nil {
			return marshalToolError(err, "evidence reference rejected"), nil
		}
		if input.ContextSeconds < 0 || input.ContextSeconds > 60 || (loc.Kind == "loki" && loc.Snapshot != "") {
			return marshalToolError(nil, "invalid evidence read or context"), nil
		}
		if loc.Kind == "loki" {
			start, _ := time.Parse(time.RFC3339Nano, loc.Query.Start)
			end, _ := time.Parse(time.RFC3339Nano, loc.Query.End)
			loc.Query.Start = start.Add(-time.Duration(input.ContextSeconds) * time.Second).Format(time.RFC3339Nano)
			loc.Query.End = end.Add(time.Duration(input.ContextSeconds) * time.Second).Format(time.RFC3339Nano)
			loc.Query.Limit = 100
			out, err := searchTelemetryLogs(ctx, loc.Query, true)
			if err != nil {
				return marshalToolError(err, "log context unavailable"), nil
			}
			return marshalTelemetryData(ctx, out, "Log timestamp and stream context; original lines subject to redaction and output budget."), nil
		}
		if loc.Kind != "tempo" || loc.TraceID == "" || s.Permission == "" {
			return marshalToolError(nil, "full trace requires explicit source-wide permission"), nil
		}
		if input.PageRunes == 0 {
			input.PageRunes = 512
		}
		if input.PageRunes < 64 || input.PageRunes > 2048 {
			return marshalToolError(nil, "page_runes must be between 64 and 2048"), nil
		}
		check, _ := json.Marshal(struct {
			Trace  string
			Scope  TelemetryQuery
			Offset int
			Size   int
		}{loc.TraceID, loc.Query, loc.Offset, input.PageRunes})
		if !contextbudget.BeginLogCheck(ctx, "read_trace\n"+string(check)) {
			return marshalToolError(nil, "duplicate read or investigation call budget exhausted"), nil
		}
		var trace map[string]any
		if err := s.getLimited(ctx, "/api/traces/"+loc.TraceID, nil, &trace, 32*1024*1024); err != nil {
			return marshalToolError(err, "trace read failed"), nil
		}
		if len(trace) == 0 {
			return marshalToolError(nil, "trace backend returned no trace payload"), nil
		}
		out, err := traceTelemetryPage(ctx, s, loc, trace, input.PageRunes)
		if err != nil {
			return marshalToolError(err, "trace page unavailable"), nil
		}
		return marshalTelemetryData(ctx, out, "Canonical redacted trace JSON fragment; concatenate ordered fragments before JSON decoding. Backend span completeness unknown."), nil
	})
}

func traceTelemetryPage(ctx context.Context, s telemetrySource, loc telemetryLocator, trace map[string]any, size int) (any, error) {
	if size == 0 {
		size = 512
	}
	if size < 64 || size > 2048 {
		return nil, fmt.Errorf("page_runes must be between 64 and 2048")
	}
	// Redact complete values before splitting, so a page boundary cannot split a secret past redaction.
	data := evidence.SanitizeUntrustedValue(mem.RedactToolPayload(trace))
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(b)
	snapshot := hex.EncodeToString(digest[:])
	if loc.Snapshot != "" && loc.Snapshot != snapshot {
		return nil, fmt.Errorf("trace changed; restart from original trace reference")
	}
	text := []rune(string(b))
	if loc.Offset < 0 || loc.Offset >= len(text) {
		return nil, fmt.Errorf("invalid trace page offset")
	}
	start := loc.Offset
	end := min(len(text), start+size)
	loc.Snapshot = snapshot
	retry, err := sealTelemetry(ctx, s, loc)
	if err != nil {
		return nil, err
	}
	next := ""
	if end < len(text) {
		loc.Offset = end
		next, err = sealTelemetry(ctx, s, loc)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"telemetry": map[string]any{
			"scope":               loc.Query,
			"backend":             "tempo",
			"trace_id":            loc.TraceID,
			"query_status":        "success",
			"representation":      "redacted_canonical_json_fragment",
			"offset_runes":        start,
			"end_runes":           end,
			"total_runes":         len(text),
			"returned":            end - start,
			"truncated":           end < len(text),
			"sampling":            "unknown",
			"missing_spans":       "unknown",
			"next_cursor":         next,
			"retry_cursor":        retry,
			"backend_byte_budget": 32 * 1024 * 1024,
		},
		"records": string(text[start:end]),
	}, nil
}

func marshalTelemetryData(ctx context.Context, out any, message string) string {
	if data, ok := out.(map[string]any); ok {
		if metadata, ok := data["telemetry"].(map[string]any); ok {
			query, _ := json.Marshal(metadata["scope"])
			digest := sha256.Sum256(query)
			metadata["query_id"] = hex.EncodeToString(digest[:])
			records, _ := json.Marshal(data["records"])
			metadata["record_bytes_before_redaction"] = len(records)
			metadata["model_output_token_budget"] = contextbudget.ToolLimits(ctx).OutputTokens
		}
	}
	return marshalToolData(out, message)
}
