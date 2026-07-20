package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
)

type Shutdown func(context.Context) error

func Init(ctx context.Context) (Shutdown, error) {
	if !configBool(ctx, "observability.enabled", false) {
		return func(context.Context) error { return nil }, nil
	}

	endpoint := configString(ctx, "observability.otlp_endpoint", "localhost:4317")
	serviceName := configString(ctx, "observability.service_name", "sreagent")
	insecure := configBool(ctx, "observability.insecure", true)

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}

	traceOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	metricOptions := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(endpoint)}
	if insecure {
		traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(configRatio(ctx)))),
	)
	metricProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(configMetricInterval(ctx)))),
	)

	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(metricProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(shutdownCtx context.Context) error {
		return errors.Join(
			metricProvider.Shutdown(shutdownCtx),
			traceProvider.Shutdown(shutdownCtx),
		)
	}, nil
}

func ExtractHTTPContext(ctx context.Context, header http.Header) context.Context {
	if header == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
}

func configRatio(ctx context.Context) float64 {
	value, err := g.Cfg().Get(ctx, "observability.trace_sample_ratio")
	if err != nil || value == nil {
		return 1
	}
	ratio := value.Float64()
	if ratio < 0 {
		return 0
	}
	if ratio > 1 {
		return 1
	}
	return ratio
}

func configMetricInterval(ctx context.Context) time.Duration {
	value, err := g.Cfg().Get(ctx, "observability.metric_interval_seconds")
	if err != nil || value == nil || value.Int() <= 0 {
		return 15 * time.Second
	}
	return time.Duration(value.Int()) * time.Second
}

func configString(ctx context.Context, key string, fallback string) string {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return fallback
	}
	configured := strings.TrimSpace(value.String())
	if configured == "" {
		return fallback
	}
	return configured
}

func configBool(ctx context.Context, key string, fallback bool) bool {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return fallback
	}
	return value.Bool()
}
