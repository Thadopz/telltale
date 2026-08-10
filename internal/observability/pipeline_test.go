package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAgentMetricsAreExported(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	defer provider.Shutdown(context.Background())

	ctx := context.Background()
	RecordAgentRun(ctx, "payment_agent", "payment", "domain", false, 25*time.Millisecond, nil)
	RecordAgentRoute(ctx, "payment_agent", "payment", errors.New("route failed"))
	RecordWatchRule(ctx, "payment-success-rate", "payment", "payment_metric", "opened", nil)
	RecordWatchCycle(ctx, 30*time.Millisecond, 3, nil)

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	names := collectedMetricNames(metrics)
	for _, expected := range []string{
		"telltale.agent.runs",
		"telltale.agent.duration",
		"telltale.agent.routes",
		"telltale.watcher.cycles",
		"telltale.watcher.cycle.duration",
		"telltale.watcher.rules",
		"telltale.watcher.rule.evaluations",
		"telltale.watcher.incident.transitions",
	} {
		if !names[expected] {
			t.Fatalf("expected metric %s, got %#v", expected, names)
		}
	}
}

func collectedMetricNames(metrics metricdata.ResourceMetrics) map[string]bool {
	names := make(map[string]bool)
	for _, scope := range metrics.ScopeMetrics {
		for _, item := range scope.Metrics {
			names[item.Name] = true
		}
	}
	return names
}
