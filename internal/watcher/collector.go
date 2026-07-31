package watcher

import (
	"SREagent/internal/ai/evidence"
	aitools "SREagent/internal/ai/tools"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

type Collector interface {
	Collect(context.Context, Rule, time.Time) (Snapshot, error)
}

type ToolCollector struct{}

func NewToolCollector() *ToolCollector {
	return &ToolCollector{}
}

func (c *ToolCollector) Collect(ctx context.Context, rule Rule, now time.Time) (Snapshot, error) {
	invokable, arguments, err := collectorTool(rule, now)
	if err != nil {
		return Snapshot{}, err
	}
	rawOutput, err := invokable.InvokableRun(ctx, arguments)
	if err != nil {
		return Snapshot{}, fmt.Errorf("invoke %s collector: %w", rule.Collector, err)
	}

	envelope := evidence.Parse(rawOutput).Envelope
	if envelope.Status != evidence.StatusSuccess {
		return Snapshot{}, fmt.Errorf("%s collector failed: %s", rule.Collector, envelope.Summary)
	}

	value, err := snapshotValue(envelope.Data, rule)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read value for rule %s: %w", rule.ID, err)
	}
	sampleCount, err := snapshotSampleCount(envelope.Data, rule)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read sample count for rule %s: %w", rule.ID, err)
	}

	return Snapshot{
		RuleID:      rule.ID,
		CollectedAt: now,
		Value:       value,
		SampleCount: sampleCount,
		EvidenceID:  envelope.EvidenceID,
		RawOutput:   rawOutput,
	}, nil
}

func collectorTool(rule Rule, now time.Time) (tool.InvokableTool, string, error) {
	switch rule.Collector {
	case CollectorPaymentMetric:
		candidate, err := aitools.NewQueryPaymentMetricsTool()
		if err != nil {
			return nil, "", err
		}
		arguments, err := json.Marshal(aitools.QueryPaymentMetricsInput{
			Service:    rule.Service,
			MetricName: rule.Metric,
			StartTime:  now.Add(-time.Duration(rule.WindowMinutes) * time.Minute).Format(time.RFC3339),
			EndTime:    now.Format(time.RFC3339),
		})
		return candidate, string(arguments), err
	case CollectorPaymentStatusDistribution:
		candidate, err := aitools.NewQueryPaymentStatusDistributionTool()
		if err != nil {
			return nil, "", err
		}
		arguments, err := json.Marshal(aitools.QueryPaymentStatusDistributionInput{Minutes: rule.WindowMinutes})
		return candidate, string(arguments), err
	case CollectorPrometheusAlerts:
		candidate, err := aitools.NewPrometheusAlertsQueryTool()
		return candidate, `{}`, err
	default:
		return nil, "", fmt.Errorf("unsupported collector %q", rule.Collector)
	}
}

func snapshotValue(data any, rule Rule) (float64, error) {
	path := rule.ValuePath
	if rule.Operator == OperatorCountAbove && path == "" {
		path = "alerts"
	}
	value, found := lookupPath(data, path)
	if !found {
		return 0, fmt.Errorf("path %q not found", path)
	}
	if rule.Operator == OperatorCountAbove {
		return collectionLength(value)
	}
	return numericValue(value)
}

func snapshotSampleCount(data any, rule Rule) (int, error) {
	if rule.SampleCountPath == "" {
		return 0, nil
	}
	value, found := lookupPath(data, rule.SampleCountPath)
	if !found {
		return 0, fmt.Errorf("path %q not found", rule.SampleCountPath)
	}
	number, err := numericValue(value)
	if err != nil {
		return 0, err
	}
	return int(number), nil
}

func lookupPath(value any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return value, true
	}
	current := value
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			next, found := lookupMapValue(typed, part)
			if !found {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func lookupMapValue(values map[string]any, key string) (any, bool) {
	if value, found := values[key]; found {
		return value, true
	}
	for existingKey, value := range values {
		if strings.EqualFold(existingKey, key) {
			return value, true
		}
	}
	return nil, false
}

func numericValue(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("value %q is not numeric", typed)
		}
		return number, nil
	default:
		return 0, fmt.Errorf("value of type %T is not numeric", value)
	}
}

func collectionLength(value any) (float64, error) {
	switch typed := value.(type) {
	case []any:
		return float64(len(typed)), nil
	case map[string]any:
		return float64(len(typed)), nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return 0, nil
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("value of type %T cannot be counted", value)
	}
}
