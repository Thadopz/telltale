package mem

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var memoryMeter = otel.Meter("telltale/memory")

var memoryFlushes = mustMemoryCounter("telltale.memory.flushes", "Completed memory buffer flushes")
var memoryFlushDuration = mustMemoryDurationHistogram("telltale.memory.flush.duration", "Memory buffer flush duration")
var memoryBufferDelay = mustMemoryDurationHistogram("telltale.memory.buffer.delay", "Delay from last buffered turn to flush")
var memoryExtractedProfiles = mustMemoryHistogram("telltale.memory.extracted.profiles", "Validated profiles per flush", "{profile}")
var memoryExtractedEvents = mustMemoryHistogram("telltale.memory.extracted.events", "Validated events per flush", "{event}")
var memoryContextRequests = mustMemoryCounter("telltale.memory.context.requests", "Profile and Event context retrievals")
var memoryContextProfiles = mustMemoryHistogram("telltale.memory.context.profiles", "Profiles included in context", "{profile}")
var memoryContextEvents = mustMemoryHistogram("telltale.memory.context.events", "Events included in context", "{event}")
var memoryMutations = mustMemoryCounter("telltale.memory.mutations", "User-confirmed memory mutations")

func recordMemoryFlush(ctx context.Context, mode string, elapsed time.Duration, delay time.Duration, profiles int, events int, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	attrs := metric.WithAttributes(attribute.String("memory.mode", mode), attribute.String("memory.status", status))
	memoryFlushes.Add(ctx, 1, attrs)
	memoryFlushDuration.Record(ctx, float64(elapsed)/float64(time.Millisecond), attrs)
	memoryBufferDelay.Record(ctx, float64(delay)/float64(time.Millisecond), attrs)
	memoryExtractedProfiles.Record(ctx, int64(profiles), attrs)
	memoryExtractedEvents.Record(ctx, int64(events), attrs)
}

func recordMemoryContext(ctx context.Context, elapsed time.Duration, profiles int, events int, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	attrs := metric.WithAttributes(attribute.String("memory.status", status))
	memoryContextRequests.Add(ctx, 1, attrs)
	memoryContextProfiles.Record(ctx, int64(profiles), attrs)
	memoryContextEvents.Record(ctx, int64(events), attrs)
}

func recordMemoryMutation(ctx context.Context, action string) {
	memoryMutations.Add(ctx, 1, metric.WithAttributes(attribute.String("memory.action", action)))
}

func mustMemoryCounter(name string, description string) metric.Int64Counter {
	counter, err := memoryMeter.Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{operation}"))
	if err != nil {
		panic(err)
	}
	return counter
}

func mustMemoryHistogram(name string, description string, unit string) metric.Int64Histogram {
	histogram, err := memoryMeter.Int64Histogram(name, metric.WithDescription(description), metric.WithUnit(unit))
	if err != nil {
		panic(err)
	}
	return histogram
}

func mustMemoryDurationHistogram(name string, description string) metric.Float64Histogram {
	histogram, err := memoryMeter.Float64Histogram(name, metric.WithDescription(description), metric.WithUnit("ms"))
	if err != nil {
		panic(err)
	}
	return histogram
}
