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

const instrumentationName = "telltale/pipeline"

var tracer = otel.Tracer(instrumentationName)
var meter = otel.Meter(instrumentationName)

var chatRequests = mustCounter("telltale.chat.requests", "Completed chat requests")
var chatDuration = mustHistogram("telltale.chat.duration", "End-to-end chat request duration")
var answerCharacters = mustCountHistogram("telltale.chat.answer.characters", "Characters in the final answer", "{character}")
var evidenceItems = mustCountHistogram("telltale.chat.evidence.items", "Evidence items used by a final answer", "{item}")
var toolFailures = mustCountHistogram("telltale.chat.tool.failures", "Failed tool calls in a chat request", "{call}")
var firstTokenDuration = mustHistogram("telltale.chat.first_token.duration", "Time to first streamed token")
var retrievalRequests = mustCounter("telltale.knowledge.retrievals", "Knowledge retrieval attempts")
var retrievalDuration = mustHistogram("telltale.knowledge.retrieval.duration", "Hybrid retrieval duration")
var retrievalCandidates = mustCountHistogram("telltale.knowledge.retrieval.candidates", "Documents returned by hybrid retrieval", "{document}")
var retrievalFinalDocuments = mustCountHistogram("telltale.knowledge.final_documents", "Documents retained after reranking and budgeting", "{document}")
var rerankRequests = mustCounter("telltale.knowledge.reranks", "Knowledge rerank attempts")
var rerankDuration = mustHistogram("telltale.knowledge.rerank.duration", "External reranker duration")
var agentRuns = mustCounter("telltale.agent.runs", "Completed coordinator and domain agent runs")
var agentDuration = mustHistogram("telltale.agent.duration", "Coordinator and domain agent run duration")
var agentRoutes = mustCounter("telltale.agent.routes", "Completed domain agent routing calls")
var watchCycles = mustCounter("telltale.watcher.cycles", "Completed anomaly watcher cycles")
var watchCycleDuration = mustHistogram("telltale.watcher.cycle.duration", "End-to-end anomaly watcher cycle duration")
var watchRulesPerCycle = mustCountHistogram("telltale.watcher.rules", "Rules evaluated per anomaly watcher cycle", "{rule}")
var watchRuleEvaluations = mustCounter("telltale.watcher.rule.evaluations", "Completed anomaly rule evaluations")
var watchIncidentTransitions = mustCounter("telltale.watcher.incident.transitions", "Incident lifecycle transitions")

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

func RecordWatchCycle(ctx context.Context, elapsed time.Duration, ruleCount int, err error) {
	attrs := metric.WithAttributes(attribute.String("watch.status", statusValue(err)))
	watchCycles.Add(ctx, 1, attrs)
	watchCycleDuration.Record(ctx, elapsedMilliseconds(elapsed), attrs)
	watchRulesPerCycle.Record(ctx, int64(ruleCount), attrs)
}

func RecordWatchRule(ctx context.Context, ruleID string, domain string, collector string, transition string, err error) {
	attrs := metric.WithAttributes(
		attribute.String("watch.rule.id", ruleID),
		attribute.String("watch.domain", domain),
		attribute.String("watch.collector", collector),
		attribute.String("watch.status", statusValue(err)),
	)
	watchRuleEvaluations.Add(ctx, 1, attrs)
	if transition != "" {
		watchIncidentTransitions.Add(ctx, 1, metric.WithAttributes(
			attribute.String("watch.rule.id", ruleID),
			attribute.String("watch.domain", domain),
			attribute.String("watch.transition", transition),
		))
	}
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
