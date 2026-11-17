package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type DiscoverMetricsInput struct {
	Query TelemetryQuery `json:"query"`
	Mode  string         `json:"mode" jsonschema:"description=names for metric names, labels for label names, values for one label's values"`
	Label string         `json:"label,omitempty"`
}

func NewDiscoverMetricsTool() (tool.InvokableTool, error) {
	return utils.InferTool("discover_metrics", "Discover Prometheus metric names, label names or label values within an authorized source/environment/service/instance and explicit time range. Limit 1 to 100. Optional query.metric restricts discovery to one exact metric. Results describe metadata, not proof of samples in range.", func(ctx context.Context, input *DiscoverMetricsInput) (string, error) {
		out, err := discoverMetrics(ctx, *input)
		if err != nil {
			return marshalToolError(err, "metric discovery unavailable"), nil
		}
		return marshalTelemetryData(ctx, out, "Scoped Prometheus metadata discovery; bounded results."), nil
	})
}

func metricDiscoveryRequest(s telemetrySource, input *DiscoverMetricsInput) (string, url.Values, error) {
	q := &input.Query
	start, end, err := telemetryRange(q)
	if err != nil {
		return "", nil, err
	}
	q.Labels = maps.Clone(q.Labels)
	if q.Metric != "" {
		if !telemetryMetricPattern.MatchString(q.Metric) {
			return "", nil, fmt.Errorf("invalid metric name")
		}
		if q.Labels == nil {
			q.Labels = map[string]string{}
		}
		if name, ok := q.Labels["__name__"]; ok && name != q.Metric {
			return "", nil, fmt.Errorf("conflicting metric name filter")
		}
		q.Labels["__name__"] = q.Metric
	}
	selector, err := telemetrySelector(s, *q)
	if err != nil {
		return "", nil, err
	}
	path := "/api/v1/labels"
	switch input.Mode {
	case "names":
		path = "/api/v1/label/__name__/values"
	case "labels":
	case "values":
		if !prometheusLabelPattern.MatchString(input.Label) {
			return "", nil, fmt.Errorf("invalid label name")
		}
		path = "/api/v1/label/" + input.Label + "/values"
	default:
		return "", nil, fmt.Errorf("mode must be names, labels or values")
	}
	return path, url.Values{
		"match[]": {selector},
		"start":   {start.Format(time.RFC3339Nano)},
		"end":     {end.Format(time.RFC3339Nano)},
		"limit":   {strconv.Itoa(q.Limit + 1)},
	}, nil
}

func discoverMetrics(ctx context.Context, input DiscoverMetricsInput) (any, error) {
	s, err := resolveTelemetry(ctx, input.Query, "prometheus")
	if err != nil {
		return nil, err
	}
	path, values, err := metricDiscoveryRequest(s, &input)
	if err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(input)
	if !contextbudget.BeginLogCheck(ctx, "discover_metrics\n"+string(canonical)) {
		return nil, fmt.Errorf("duplicate discovery or investigation call budget exhausted")
	}
	var result struct {
		Status   string   `json:"status"`
		Data     []string `json:"data"`
		Warnings []string `json:"warnings"`
	}
	if err := s.get(ctx, path, values, &result); err != nil {
		return nil, err
	}
	if result.Status != "success" || result.Data == nil {
		return nil, fmt.Errorf("Prometheus metadata query failed or malformed response")
	}
	slices.Sort(result.Data)
	truncated := len(result.Data) > input.Query.Limit || len(result.Warnings) > 0
	result.Data = result.Data[:min(len(result.Data), input.Query.Limit)]
	return map[string]any{
		"telemetry": map[string]any{
			"scope":               input.Query,
			"backend":             "prometheus",
			"query":               values.Get("match[]"),
			"query_status":        "success",
			"representation":      "metadata_" + input.Mode,
			"label":               input.Label,
			"matched":             "unknown",
			"returned":            len(result.Data),
			"truncated":           truncated,
			"warnings":            result.Warnings,
			"reason":              "metadata may include series without samples in range; narrow metric, label filters or time when truncated",
			"backend_byte_budget": 2 * 1024 * 1024,
		},
		"records": result.Data,
	}, nil
}
