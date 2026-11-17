package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/gogf/gf/v2/frame/g"
)

var defaultPaymentAgentBaseURL = "http://127.0.0.1:8000"

type paymentObservationBuilder func(payload any) []evidence.Observation

type QueryPaymentAnomalyInput struct {
	Type           string `json:"type,omitempty" jsonschema:"description=Anomaly type filter, such as PAYMENT_FAILURE_RATE_HIGH, ORDER_SUCCESS_RATE_LOW, PAYMENT_STATE_MISMATCH, ORDER_PENDING_TOO_LONG"`
	Status         string `json:"status,omitempty" jsonschema:"description=Anomaly status filter, usually OPEN"`
	Severity       string `json:"severity,omitempty" jsonschema:"description=Severity filter, such as WARNING or CRITICAL"`
	RelatedOrderID string `json:"related_order_id,omitempty" jsonschema:"description=Payment order number to filter anomalies for a specific order"`
	Limit          int    `json:"limit,omitempty" jsonschema:"description=Maximum events to return; default is server-side default"`
}

type QueryPaymentMetricsInput struct {
	Service    string `json:"service,omitempty" jsonschema:"description=Service name, defaults to payment"`
	MetricName string `json:"metric_name,omitempty" jsonschema:"description=Metric name such as order_create_request, order_create_success, payment_success, payment_failed"`
	StartTime  string `json:"start_time,omitempty" jsonschema:"description=Start time, accepted formats include YYYY-MM-DD HH:mm:ss, YYYY-MM-DDTHH:mm:ss, or RFC3339"`
	EndTime    string `json:"end_time,omitempty" jsonschema:"description=End time, accepted formats include YYYY-MM-DD HH:mm:ss, YYYY-MM-DDTHH:mm:ss, or RFC3339"`
}

type PaymentOrderDetailInput struct {
	PaymentOrderNo string `json:"payment_order_no" jsonschema:"description=Payment order number to inspect"`
}

type SearchPaymentLogsInput struct {
	StartTime string `json:"start_time,omitempty" jsonschema:"description=Optional RFC3339 inclusive start; requires end_time; new backend required"`
	EndTime   string `json:"end_time,omitempty" jsonschema:"description=Optional RFC3339 exclusive end; at most one hour"`
	Operation string `json:"operation,omitempty" jsonschema:"description=Optional exact operation filter; new backend required"`
	OrderID   string `json:"order_id,omitempty" jsonschema:"description=Payment order number to filter diagnostic logs"`
	Level     string `json:"level,omitempty" jsonschema:"description=Log level such as INFO, WARN, or ERROR. Empty defaults to recent ERROR logs when no other filter is provided"`
	Keyword   string `json:"keyword,omitempty" jsonschema:"description=Keyword to search in log message or error message"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum logs to return"`
}

type QueryPaymentStatusDistributionInput struct {
	Minutes int `json:"minutes,omitempty" jsonschema:"description=Recent time window in minutes; defaults to server-side default"`
}

func NewQueryPaymentAnomalyTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"query_payment_anomaly",
		"Query payment/order anomaly events from the local payment observability agent. Use this when the user asks whether payment is abnormal, whether there are open incidents, or needs evidence for business anomalies.",
		func(ctx context.Context, input *QueryPaymentAnomalyInput, opts ...tool.Option) (string, error) {
			values := url.Values{}
			if input != nil {
				addQuery(values, "type", input.Type)
				addQuery(values, "status", input.Status)
				addQuery(values, "severity", input.Severity)
				addQuery(values, "relatedOrderId", input.RelatedOrderID)
				addPositiveIntQuery(values, "limit", input.Limit)
			}
			return callPaymentAgent(ctx, "/api/v1/agent/query_anomaly_event", values, "payment anomaly query completed", paymentAnomalyObservations(input))
		})
}

func NewQueryPaymentMetricsTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"query_payment_metrics",
		"Query payment business metrics from the local payment observability agent, including order creation and payment success/failure rates.",
		func(ctx context.Context, input *QueryPaymentMetricsInput, opts ...tool.Option) (string, error) {
			values := url.Values{}
			service := "payment"
			if input != nil {
				if strings.TrimSpace(input.Service) != "" {
					service = input.Service
				}
				addQuery(values, "metricName", input.MetricName)
				addQuery(values, "startTime", input.StartTime)
				addQuery(values, "endTime", input.EndTime)
			}
			addQuery(values, "service", service)
			return callPaymentAgent(ctx, "/api/v1/agent/query_business_metric", values, "payment metrics query completed", paymentMetricObservations(input, service))
		})
}

func NewQueryPaymentOrderDetailTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"query_payment_order_detail",
		"Query payment order, latest transaction, refunds, and notify tasks from the local payment observability agent. Use this for order-level diagnosis.",
		func(ctx context.Context, input *PaymentOrderDetailInput, opts ...tool.Option) (string, error) {
			orderNo, err := requirePaymentOrderNo(input)
			if err != nil {
				return marshalToolError(err, "invalid payment order input"), nil
			}
			values := url.Values{}
			addQuery(values, "paymentOrderNo", orderNo)
			return callPaymentAgent(ctx, "/api/v1/agent/query_order_detail", values, "payment order detail query completed", paymentOrderObservations(orderNo))
		})
}

func NewCheckPaymentConsistencyTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"check_payment_consistency",
		"Check whether a payment order and its latest transaction are internally consistent. Use this for state mismatch diagnosis.",
		func(ctx context.Context, input *PaymentOrderDetailInput, opts ...tool.Option) (string, error) {
			orderNo, err := requirePaymentOrderNo(input)
			if err != nil {
				return marshalToolError(err, "invalid payment order input"), nil
			}
			values := url.Values{}
			addQuery(values, "paymentOrderNo", orderNo)
			return callPaymentAgent(ctx, "/api/v1/agent/check_order_consistency", values, "payment consistency check completed", paymentConsistencyObservations(orderNo))
		})
}

func NewSearchPaymentLogsTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"search_payment_logs",
		"Search structured diagnostic logs from the local payment observability agent. Use this to find payment.create, payment.callback, and error logs for an order or incident.",
		func(ctx context.Context, input *SearchPaymentLogsInput, opts ...tool.Option) (string, error) {
			values := url.Values{}
			if input != nil {
				if input.StartTime != "" || input.EndTime != "" {
					if err := validateLogWindow(&AnalyzeLogWindowInput{Service: "payment", Start: input.StartTime, End: input.EndTime, Operation: input.Operation}); err != nil {
						return marshalToolError(err, "invalid log window"), nil
					}
					addQuery(values, "startTime", input.StartTime)
					addQuery(values, "endTime", input.EndTime)
				}
				addQuery(values, "operation", input.Operation)
				addQuery(values, "orderId", input.OrderID)
				addQuery(values, "level", input.Level)
				addQuery(values, "keyword", input.Keyword)
				addPositiveIntQuery(values, "limit", input.Limit)
			}
			output, err := callPaymentAgent(ctx, "/api/v1/agent/search_error_logs", values, "payment diagnostic logs query completed", paymentLogObservations(input))
			if err == nil && input != nil && (input.StartTime != "" || input.EndTime != "" || input.Operation != "") {
				env := evidence.Parse(output).Envelope
				payload, _ := env.Data.(map[string]any)
				data, _ := payload["data"].(map[string]any)
				scope, _ := data["applied_scope"].(map[string]any)
				if scope == nil || scope["start_time"] != input.StartTime || scope["end_time"] != input.EndTime || scope["operation"] != input.Operation {
					return marshalToolError(nil, "backend cannot verify requested log scope; upgrade backend or report capability gap"), nil
				}
			}
			return output, err
		})
}

func NewQueryPaymentStatusDistributionTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"query_payment_status_distribution",
		"Query recent payment order status distribution from the local payment observability agent. Use this to estimate current impact scope.",
		func(ctx context.Context, input *QueryPaymentStatusDistributionInput, opts ...tool.Option) (string, error) {
			values := url.Values{}
			if input != nil {
				addPositiveIntQuery(values, "minutes", input.Minutes)
			}
			return callPaymentAgent(ctx, "/api/v1/agent/query_recent_status_distribution", values, "payment status distribution query completed", paymentStatusDistributionObservations(input))
		})
}

func callPaymentAgent(ctx context.Context, path string, values url.Values, message string, buildObservations paymentObservationBuilder) (string, error) {
	endpoint, err := paymentAgentURL(ctx, path, values)
	if err != nil {
		return marshalToolError(err, "build payment agent request failed"), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return marshalToolError(err, "build payment agent request failed"), nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return marshalToolError(err, "call payment agent failed"), nil
	}
	defer resp.Body.Close()
	const maxPaymentResponseBytes = 2 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPaymentResponseBytes+1))
	if err != nil {
		return marshalToolError(err, "read payment agent response failed"), nil
	}
	if len(body) > maxPaymentResponseBytes {
		return marshalToolError(nil, "payment agent response exceeds byte budget; use aggregation or narrower scope"), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return marshalToolError(fmt.Errorf("payment agent http %d", resp.StatusCode), "payment agent returned non-success status"), nil
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return marshalToolError(err, "parse payment agent response failed"), nil
	}
	observations := []evidence.Observation{}
	if buildObservations != nil {
		observations = buildObservations(payload)
	}
	return marshalToolEvidence(payload, message, observations), nil
}

func paymentAnomalyObservations(input *QueryPaymentAnomalyInput) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationMetricAnomaly
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		fields := map[string]any{}
		if input != nil {
			setAny(fields, "type", input.Type)
			setAny(fields, "status", input.Status)
			setAny(fields, "severity", input.Severity)
			setAny(fields, "order_no", input.RelatedOrderID)
			if input.Limit > 0 {
				fields["limit"] = input.Limit
			}
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "payment", Service: "payment", OrderNo: stringField(fields, "order_no")},
			Fields:  fields,
			Summary: "payment anomaly query observation",
		}}
	}
}

func paymentMetricObservations(input *QueryPaymentMetricsInput, service string) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationMetricValue
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		fields := map[string]any{"service": service}
		scope := evidence.EvidenceScope{}
		if input != nil {
			setAny(fields, "metric_name", input.MetricName)
			scope.StartTime = strings.TrimSpace(input.StartTime)
			scope.EndTime = strings.TrimSpace(input.EndTime)
		}
		if metricName := strings.ToLower(stringField(fields, "metric_name")); strings.Contains(metricName, "fail") || strings.Contains(metricName, "error") {
			kind = evidence.ObservationMetricAnomaly
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "payment", Service: service},
			Scope:   scope,
			Fields:  fields,
			Summary: "payment metric observation",
		}}
	}
}

func paymentOrderObservations(orderNo string) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationOrderState
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "order", Service: "payment", OrderNo: orderNo},
			Fields:  map[string]any{"order_no": orderNo},
			Summary: "payment order state observation",
		}}
	}
}

func paymentConsistencyObservations(orderNo string) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationStateMismatch
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "order", Service: "payment", OrderNo: orderNo},
			Fields:  map[string]any{"order_no": orderNo},
			Summary: "payment consistency observation",
		}}
	}
}

func paymentLogObservations(input *SearchPaymentLogsInput) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationLogError
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		fields := map[string]any{}
		subject := evidence.SubjectRef{Type: "log", Service: "payment"}
		if input != nil {
			setAny(fields, "order_no", input.OrderID)
			setAny(fields, "level", input.Level)
			setAny(fields, "error_code", input.Keyword)
			subject.OrderNo = strings.TrimSpace(input.OrderID)
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: subject,
			Fields:  fields,
			Summary: "payment log error observation",
		}}
	}
}

func paymentStatusDistributionObservations(input *QueryPaymentStatusDistributionInput) paymentObservationBuilder {
	return func(payload any) []evidence.Observation {
		kind := evidence.ObservationStatusDistribution
		if isEmptyPayload(payload) {
			kind = evidence.ObservationEmptyResult
		}
		fields := map[string]any{"service": "payment"}
		if input != nil && input.Minutes > 0 {
			fields["minutes"] = input.Minutes
		}
		return []evidence.Observation{{
			Kind:    kind,
			Subject: evidence.SubjectRef{Type: "payment", Service: "payment"},
			Fields:  fields,
			Summary: "payment status distribution observation",
		}}
	}
}

func paymentAgentURL(ctx context.Context, path string, values url.Values) (string, error) {
	base := paymentAgentBaseURL(ctx)
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = values.Encode()
	return u.String(), nil
}

func paymentAgentBaseURL(ctx context.Context) string {
	for _, key := range []string{"tools.payment_agent.base_url", "payment_agent.base_url"} {
		v, err := g.Cfg().Get(ctx, key)
		if err != nil || v == nil {
			continue
		}
		base := strings.TrimSpace(v.String())
		if base != "" {
			return strings.TrimRight(base, "/")
		}
	}
	return defaultPaymentAgentBaseURL
}

func addQuery(values url.Values, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values.Set(key, value)
	}
}

func addPositiveIntQuery(values url.Values, key string, value int) {
	if value > 0 {
		values.Set(key, strconv.Itoa(value))
	}
}

func requirePaymentOrderNo(input *PaymentOrderDetailInput) (string, error) {
	if input == nil {
		return "", fmt.Errorf("missing payment order input")
	}
	orderNo := strings.TrimSpace(input.PaymentOrderNo)
	if orderNo == "" {
		return "", fmt.Errorf("missing payment_order_no")
	}
	return orderNo, nil
}

func setAny(fields map[string]any, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		fields[key] = value
	}
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	if raw, ok := fields[key].(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func isEmptyPayload(payload any) bool {
	if payload == nil {
		return true
	}
	switch v := payload.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		if len(v) == 0 {
			return true
		}
		for _, key := range []string{"data", "items", "list", "records", "rows", "events", "logs"} {
			if raw, ok := v[key]; ok {
				return isEmptyPayload(raw)
			}
		}
		return false
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}
