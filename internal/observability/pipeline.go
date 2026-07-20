package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "sreagent/pipeline"

var tracer = otel.Tracer(instrumentationName)
var meter = otel.Meter(instrumentationName)

var chatRequests = mustCounter("sreagent.chat.requests", "Completed chat requests")
var chatDuration = mustHistogram("sreagent.chat.duration", "End-to-end chat request duration")
var answerCharacters = mustCountHistogram("sreagent.chat.answer.characters", "Characters in the final answer", "{character}")
var evidenceItems = mustCountHistogram("sreagent.chat.evidence.items", "Evidence items used by a final answer", "{item}")
var toolFailures = mustCountHistogram("sreagent.chat.tool.failures", "Failed tool calls in a chat request", "{call}")
var firstTokenDuration = mustHistogram("sreagent.chat.first_token.duration", "Time to first streamed token")
var retrievalRequests = mustCounter("sreagent.knowledge.retrievals", "Knowledge retrieval attempts")
var retrievalDuration = mustHistogram("sreagent.knowledge.retrieval.duration", "Hybrid retrieval duration")
var retrievalCandidates = mustCountHistogram("sreagent.knowledge.retrieval.candidates", "Documents returned by hybrid retrieval", "{document}")
var retrievalFinalDocuments = mustCountHistogram("sreagent.knowledge.final_documents", "Documents retained after reranking and budgeting", "{document}")
var rerankRequests = mustCounter("sreagent.knowledge.reranks", "Knowledge rerank attempts")
var rerankDuration = mustHistogram("sreagent.knowledge.rerank.duration", "External reranker duration")
var agentRuns = mustCounter("sreagent.agent.runs", "Completed coordinator and domain agent runs")
var agentDuration = mustHistogram("sreagent.agent.duration", "Coordinator and domain agent run duration")
var agentRoutes = mustCounter("sreagent.agent.routes", "Completed domain agent routing calls")

func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

func EndSpan(span trace.Span, err error, attrs ...attribute.KeyValue) {
	if span == nil {
		return
	}
	span.SetAttributes(attrs...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

func RecordChat(ctx context.Context, stream bool, elapsed time.Duration, err error, answerLength int, evidenceCount int, failedTools int) {
	status := statusValue(err)
	attrs := metric.WithAttributes(
		attribute.Bool("chat.stream", stream),
		attribute.String("chat.status", status),
	)
	chatRequests.Add(ctx, 1, attrs)
	chatDuration.Record(ctx, elapsedMilliseconds(elapsed), attrs)
	answerCharacters.Record(ctx, int64(answerLength), attrs)
	evidenceItems.Record(ctx, int64(evidenceCount), attrs)
	toolFailures.Record(ctx, int64(failedTools), attrs)
}

func RecordFirstToken(ctx context.Context, elapsed time.Duration) {
	firstTokenDuration.Record(ctx, elapsedMilliseconds(elapsed), metric.WithAttributes(attribute.Bool("chat.stream", true)))
}

func RecordRetrieval(ctx context.Context, elapsed time.Duration, err error, candidates int, finalDocuments int) {
	attrs := metric.WithAttributes(attribute.String("retrieval.status", statusValue(err)))
	retrievalRequests.Add(ctx, 1, attrs)
	retrievalDuration.Record(ctx, elapsedMilliseconds(elapsed), attrs)
	retrievalCandidates.Record(ctx, int64(candidates), attrs)
	retrievalFinalDocuments.Record(ctx, int64(finalDocuments), attrs)
}

func RecordRerank(ctx context.Context, elapsed time.Duration, err error) {
	attrs := metric.WithAttributes(attribute.String("rerank.status", statusValue(err)))
	rerankRequests.Add(ctx, 1, attrs)
	rerankDuration.Record(ctx, elapsedMilliseconds(elapsed), attrs)
}

func RecordAgentRun(ctx context.Context, name string, domain string, kind string, streaming bool, elapsed time.Duration, err error) {
	attrs := metric.WithAttributes(
		attribute.String("agent.name", name),
		attribute.String("agent.domain", domain),
		attribute.String("agent.kind", kind),
		attribute.String("agent.status", statusValue(err)),
		attribute.Bool("agent.streaming", streaming),
	)
	agentRuns.Add(ctx, 1, attrs)
	agentDuration.Record(ctx, elapsedMilliseconds(elapsed), attrs)
}

func RecordAgentRoute(ctx context.Context, name string, domain string, err error) {
	agentRoutes.Add(ctx, 1, metric.WithAttributes(
		attribute.String("agent.target.name", name),
		attribute.String("agent.target.domain", domain),
		attribute.String("agent.route.status", statusValue(err)),
	))
}

func mustCounter(name string, description string) metric.Int64Counter {
	counter, err := meter.Int64Counter(name, metric.WithDescription(description), metric.WithUnit("{request}"))
	if err != nil {
		panic(err)
	}
	return counter
}

func mustHistogram(name string, description string) metric.Float64Histogram {
	histogram, err := meter.Float64Histogram(name, metric.WithDescription(description), metric.WithUnit("ms"))
	if err != nil {
		panic(err)
	}
	return histogram
}

func mustCountHistogram(name string, description string, unit string) metric.Int64Histogram {
	histogram, err := meter.Int64Histogram(name, metric.WithDescription(description), metric.WithUnit(unit))
	if err != nil {
		panic(err)
	}
	return histogram
}

func statusValue(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func elapsedMilliseconds(elapsed time.Duration) float64 {
	return float64(elapsed) / float64(time.Millisecond)
}
