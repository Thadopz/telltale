package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	maxPrometheusMatchLabels = 8
	maxPrometheusLabelRunes  = 128
	maxPrometheusAlerts      = 50
	maxAlertDescriptionRunes = 1024
)

var prometheusLabelPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type QueryPrometheusAlertsInput struct {
	MatchLabels map[string]string `json:"match_labels,omitempty" jsonschema:"description=Exact Prometheus label matches; all supplied labels must match"`
	States      []string          `json:"states,omitempty" jsonschema:"description=Optional alert states to include: firing or pending"`
}

type PrometheusAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    string            `json:"activeAt"`
	Value       string            `json:"value"`
}

type PrometheusAlertsResult struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []PrometheusAlert `json:"alerts"`
	} `json:"data"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

type SimplifiedAlert struct {
	AlertName   string            `json:"alert_name" jsonschema:"description=Alert name from the Prometheus alertname label"`
	Description string            `json:"description" jsonschema:"description=Alert description"`
	State       string            `json:"state" jsonschema:"description=Prometheus alert state"`
	ActiveAt    string            `json:"active_at" jsonschema:"description=RFC3339 alert activation time"`
	Duration    string            `json:"duration" jsonschema:"description=How long the alert has been active"`
	Labels      map[string]string `json:"labels,omitempty" jsonschema:"description=Bounded labels retained for service and environment correlation"`
}

type PrometheusAlertsOutput struct {
	Matched   int               `json:"matched"`
	Returned  int               `json:"returned"`
	Truncated bool              `json:"truncated"`
	Success   bool              `json:"success"`
	Alerts    []SimplifiedAlert `json:"alerts"`
	Message   string            `json:"message,omitempty"`
	Error     string            `json:"error,omitempty"`
}

type prometheusAlertFilter struct {
	matchLabels map[string]string
	states      map[string]bool
}

func queryPrometheusAlerts(ctx context.Context) (PrometheusAlertsResult, error) {
	var result PrometheusAlertsResult
	apiURL := fmt.Sprintf("%s/api/v1/alerts", prometheusBaseURL(ctx))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return result, fmt.Errorf("build Prometheus alerts request: %w", err)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return result, fmt.Errorf("query Prometheus alerts: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("Prometheus alerts returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return result, fmt.Errorf("read Prometheus alerts response: %w", err)
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("parse Prometheus alerts response: %w", err)
	}
	if result.Status != "success" {
		return result, fmt.Errorf("Prometheus alerts query failed: %s", result.Error)
	}
	return result, nil
}

func calculateDuration(activeAtText string) string {
	activeAt, err := time.Parse(time.RFC3339Nano, activeAtText)
	if err != nil {
		return "unknown"
	}
	duration := time.Since(activeAt)
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func NewPrometheusAlertsQueryTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"query_prometheus_alerts",
		"Query active alerts from Prometheus. Optional exact label and state filters can limit the result to one service.",
		func(ctx context.Context, input *QueryPrometheusAlertsInput, opts ...tool.Option) (string, error) {
			filter, err := normalizePrometheusAlertFilter(input)
			if err != nil {
				return marshalToolError(err, "invalid Prometheus alert filter"), nil
			}
			result, err := queryPrometheusAlerts(ctx)
			if err != nil {
				return marshalToolError(err, "failed to query Prometheus alerts"), nil
			}
			alerts := simplifyPrometheusAlerts(result.Data.Alerts, filter)
			matched := 0
			for _, alert := range result.Data.Alerts {
				if prometheusAlertMatches(alert, filter) {
					matched++
				}
			}
			log.Printf("Prometheus alerts query completed: %d alerts found", len(alerts))
			output := PrometheusAlertsOutput{
				Matched:   matched,
				Returned:  len(alerts),
				Truncated: matched > len(alerts),
				Success:   true,
				Alerts:    alerts,
				Message:   fmt.Sprintf("Successfully retrieved %d active alerts", len(alerts)),
			}
			return marshalToolData(output, fmt.Sprintf("retrieved %d active Prometheus alerts", len(alerts))), nil
		})
}

func simplifyPrometheusAlerts(alerts []PrometheusAlert, filter prometheusAlertFilter) []SimplifiedAlert {
	result := make([]SimplifiedAlert, 0, len(alerts))
	for _, alert := range alerts {
		if !prometheusAlertMatches(alert, filter) {
			continue
		}
		alertName := alert.Labels["alertname"]
		result = append(result, SimplifiedAlert{
			AlertName:   boundedText(alertName, maxPrometheusLabelRunes),
			Description: boundedText(alert.Annotations["description"], maxAlertDescriptionRunes),
			State:       alert.State,
			ActiveAt:    alert.ActiveAt,
			Duration:    calculateDuration(alert.ActiveAt),
			Labels:      boundedPrometheusLabels(alert.Labels),
		})
		if len(result) >= maxPrometheusAlerts {
			break
		}
	}
	return result
}

func normalizePrometheusAlertFilter(input *QueryPrometheusAlertsInput) (prometheusAlertFilter, error) {
	filter := prometheusAlertFilter{matchLabels: map[string]string{}, states: map[string]bool{}}
	if input == nil {
		return filter, nil
	}
	if len(input.MatchLabels) > maxPrometheusMatchLabels {
		return filter, fmt.Errorf("match_labels exceeds %d entries", maxPrometheusMatchLabels)
	}
	for rawKey, rawValue := range input.MatchLabels {
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)
		if !prometheusLabelPattern.MatchString(key) {
			return filter, fmt.Errorf("invalid Prometheus label name %q", key)
		}
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxPrometheusLabelRunes || hasControlCharacter(value) {
			return filter, fmt.Errorf("invalid value for Prometheus label %q", key)
		}
		filter.matchLabels[key] = value
	}
	for _, rawState := range input.States {
		state := strings.ToLower(strings.TrimSpace(rawState))
		if state != "firing" && state != "pending" {
			return filter, fmt.Errorf("unsupported Prometheus alert state %q", rawState)
		}
		filter.states[state] = true
	}
	return filter, nil
}

func prometheusAlertMatches(alert PrometheusAlert, filter prometheusAlertFilter) bool {
	for key, expected := range filter.matchLabels {
		if alert.Labels[key] != expected {
			return false
		}
	}
	state := strings.ToLower(strings.TrimSpace(alert.State))
	return len(filter.states) == 0 || filter.states[state]
}

func boundedPrometheusLabels(labels map[string]string) map[string]string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxPrometheusMatchLabels {
		keys = keys[:maxPrometheusMatchLabels]
	}
	bounded := make(map[string]string, len(keys))
	for _, key := range keys {
		bounded[key] = boundedLabelValue(labels[key])
	}
	return bounded
}

func boundedLabelValue(value string) string {
	return boundedText(value, maxPrometheusLabelRunes)
}

func boundedText(value string, maxRunes int) string {
	var builder strings.Builder
	runeCount := 0
	for _, character := range value {
		if unicode.IsControl(character) {
			continue
		}
		if runeCount >= maxRunes {
			break
		}
		builder.WriteRune(character)
		runeCount++
	}
	return builder.String()
}

func hasControlCharacter(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func prometheusBaseURL(ctx context.Context) string {
	value, err := g.Cfg().Get(ctx, "tools.prometheus.base_url")
	if err == nil && value != nil {
		baseURL := strings.TrimRight(strings.TrimSpace(value.String()), "/")
		if baseURL != "" {
			return baseURL
		}
	}
	return "http://127.0.0.1:9090"
}
