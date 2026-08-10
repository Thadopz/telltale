package sqlobserve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func NewConfiguredObserver(ctx context.Context) (*Observer, error) {
	budget := LoadResultBudget(ctx)
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &Observer{
		Budget:         budget,
		ResolveAdapter: configuredAdapterForTarget,
	}, nil
}

func NewObserverWithExecutor(factory ExecutorFactory, budget ResultBudget) (*Observer, error) {
	if factory == nil {
		return nil, fmt.Errorf("sql observer executor factory is required")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &Observer{Budget: budget, OpenExecutor: factory, Adapter: newMysqlAdapter()}, nil
}

func NewObserverWithAdapter(adapter DatabaseAdapter, budget ResultBudget) (*Observer, error) {
	if adapter == nil {
		return nil, fmt.Errorf("sql observer database adapter is required")
	}
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	return &Observer{Budget: budget, Adapter: adapter}, nil
}

func (observer *Observer) Query(ctx context.Context, request QueryRequest) (evidence.Envelope, error) {
	if observer == nil {
		return evidence.Envelope{}, fmt.Errorf("sql observer is not configured")
	}

	request.Target = configuredTargetName(ctx, request.Target)
	adapter, err := observer.adapterFor(ctx, request.Target)
	if err != nil {
		return evidence.Envelope{}, err
	}
	plan, err := adapter.BuildPlan(request, observer.Budget)
	if err != nil {
		return evidence.Envelope{}, err
	}
	plan.Engine = adapter.Engine()
	plan.Capability = capabilityForQueryKind(request.Kind)
	plan.Target = request.Target
	if !adapter.Supports(request.Kind) {
		return unsupportedCapabilityEnvelope(plan), nil
	}

	executor, err := observer.openExecutor(ctx, request.Target, adapter)
	if err != nil {
		return queryErrorEnvelope(err, plan), nil
	}
	defer executor.Close()

	rows, err := executor.Query(ctx, plan.SQL)
	if err != nil {
		return queryErrorEnvelope(err, plan), nil
	}
	rows = projectRows(rows, plan.Projection)

	maxBytes := request.MaxBytes
	if maxBytes <= 0 {
		maxBytes = observer.Budget.AgentMaxBytes
	}
	if maxBytes > observer.Budget.CLIMaxBytes {
		maxBytes = observer.Budget.CLIMaxBytes
	}
	reducedRows, metadata, err := ReduceRows(rows, plan.Limit, maxBytes, observer.Budget.MaxSQLTextChars)
	if err != nil {
		return resultTooLargeEnvelope(plan, maxBytes), nil
	}
	metadata.Projection = append([]string(nil), plan.Projection...)
	metadata.SortBy = plan.SortBy

	if len(reducedRows) == 0 {
		return emptyResultEnvelope(plan, metadata), nil
	}

	return observer.buildEnvelopeWithinBudget(plan, reducedRows, metadata, maxBytes), nil
}

func (observer *Observer) adapterFor(ctx context.Context, target string) (DatabaseAdapter, error) {
	if observer.Adapter != nil {
		return observer.Adapter, nil
	}
	if observer.ResolveAdapter == nil {
		return nil, fmt.Errorf("sql observer database adapter is not configured")
	}
	return observer.ResolveAdapter(ctx, target)
}

func (observer *Observer) openExecutor(ctx context.Context, target string, adapter DatabaseAdapter) (QueryExecutor, error) {
	if observer.OpenExecutor != nil {
		return observer.OpenExecutor(ctx, target)
	}
	return adapter.OpenExecutor(ctx, target)
}

func projectRows(rows []map[string]any, projection []string) []map[string]any {
	if len(projection) == 0 {
		return rows
	}
	projected := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		copyRow := make(map[string]any, len(projection))
		for _, field := range projection {
			if value, ok := row[field]; ok {
				copyRow[field] = value
			}
		}
		projected = append(projected, copyRow)
	}
	return projected
}

func (observer *Observer) buildEnvelopeWithinBudget(plan QueryPlan, rows []map[string]any, metadata evidence.ResultMetadata, maxBytes int) evidence.Envelope {
	for len(rows) > 0 {
		metadata.ReturnedRows = len(rows)
		metadata.ReturnedBytes = encodedRowsBytes(rows)
		observations := buildObservations(plan, rows, observer.Budget.MaxObservations)
		data := map[string]any{
			"kind":            string(plan.Kind),
			"source":          plan.Source,
			"database_engine": string(plan.Engine),
			"target":          plan.Target,
			"capability":      plan.Capability,
			"rows":            rows,
		}
		message := fmt.Sprintf("%s query returned %d rows", string(plan.Kind), len(rows))
		envelope := evidence.NewDataWithResultMetadata(data, message, observations, metadata)
		if len(evidence.Marshal(envelope)) <= maxBytes {
			return envelope
		}
		metadata.Truncated = true
		metadata.TruncationReason = appendReason(metadata.TruncationReason, "max_bytes")
		rows = rows[:len(rows)-1]
	}

	return resultTooLargeEnvelope(plan, maxBytes)
}

func buildObservations(plan QueryPlan, rows []map[string]any, maxObservations int) []evidence.Observation {
	if plan.ObservationKind == "" || maxObservations <= 0 {
		return nil
	}
	if len(rows) > maxObservations {
		rows = rows[:maxObservations]
	}
	observations := make([]evidence.Observation, 0, len(rows))
	for index, row := range rows {
		fields := make(map[string]any, len(row))
		for key, value := range row {
			fields[key] = value
		}
		observations = append(observations, evidence.Observation{
			Kind:    plan.ObservationKind,
			Scope:   evidence.EvidenceScope{Fields: queryScopeFields(plan)},
			Fields:  fields,
			Summary: fmt.Sprintf("%s observation row %d", string(plan.Kind), index+1),
		})
	}
	return observations
}

func queryScopeFields(plan QueryPlan) map[string]string {
	fields := map[string]string{
		"source":          string(plan.Source),
		"limit":           fmt.Sprintf("%d", plan.Limit),
		"database_engine": string(plan.Engine),
		"target":          plan.Target,
		"capability":      plan.Capability,
	}
	if plan.Window != "" {
		fields["window"] = plan.Window
	}
	if plan.SortBy != "" {
		fields["sort_by"] = plan.SortBy
	}
	return fields
}

func encodedRowsBytes(rows []map[string]any) int {
	encoded, err := json.Marshal(rows)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func emptyResultEnvelope(plan QueryPlan, metadata evidence.ResultMetadata) evidence.Envelope {
	metadata.ReturnedRows = 0
	envelope := evidence.NewDataWithResultMetadata(
		map[string]any{
			"kind":            string(plan.Kind),
			"source":          plan.Source,
			"database_engine": string(plan.Engine),
			"target":          plan.Target,
			"capability":      plan.Capability,
			"rows":            []map[string]any{},
		},
		fmt.Sprintf("%s query returned no rows", string(plan.Kind)),
		nil,
		metadata,
	)
	envelope.EvidenceID = ""
	envelope.CanUseAsEvidence = false
	envelope.DataStatus = evidence.DataEmpty
	envelope.FailureReason = "empty_result"
	return envelope
}

func resultTooLargeEnvelope(plan QueryPlan, maxBytes int) evidence.Envelope {
	envelope := evidence.NewError(nil, fmt.Sprintf("%s query result exceeded the configured budget", string(plan.Kind)))
	envelope.Status = evidence.StatusUnknown
	envelope.ErrorCode = "RESULT_BUDGET_EXCEEDED"
	envelope.FailureReason = "result_row_exceeds_budget"
	envelope.DataStatus = evidence.DataUnknown
	envelope.ResultMetadata = evidence.ResultMetadata{
		MaxRows:          plan.Limit,
		MaxBytes:         maxBytes,
		Truncated:        true,
		TruncationReason: "result_row_exceeds_budget",
		Projection:       append([]string(nil), plan.Projection...),
		SortBy:           plan.SortBy,
	}
	envelope.Data = map[string]any{
		"database_engine": string(plan.Engine),
		"target":          plan.Target,
		"capability":      plan.Capability,
	}
	envelope.Message = fmt.Sprintf("query result exceeded max_bytes=%d", maxBytes)
	envelope.Summary = envelope.Message
	return envelope
}

func unsupportedCapabilityEnvelope(plan QueryPlan) evidence.Envelope {
	envelope := evidence.NewError(nil, fmt.Sprintf("database capability %s is not supported by %s", plan.Capability, plan.Engine))
	envelope.Status = evidence.StatusUnknown
	envelope.DataStatus = evidence.DataUnknown
	envelope.ErrorCode = "CAPABILITY_NOT_SUPPORTED"
	envelope.FailureReason = "capability_not_supported"
	envelope.CanUseAsEvidence = false
	envelope.EvidenceID = ""
	envelope.Data = map[string]any{
		"database_engine": plan.Engine,
		"target":          plan.Target,
		"capability":      plan.Capability,
	}
	envelope.Summary = envelope.Message
	return envelope
}

func capabilityForQueryKind(kind QueryKind) string {
	switch kind {
	case QueryServerHealth:
		return "server_health"
	case QueryActiveQueries:
		return "active_queries"
	case QuerySlowSQL:
		return "slow_queries"
	case QueryWaitEvents:
		return "wait_events"
	case QueryLocks:
		return "locks"
	case QueryRawRead:
		return "raw_read"
	default:
		return string(kind)
	}
}

func queryErrorEnvelope(err error, plan QueryPlan) evidence.Envelope {
	envelope := evidence.NewError(err, fmt.Sprintf("%s query failed", string(plan.Kind)))
	envelope.Data = map[string]any{
		"database_engine": string(plan.Engine),
		"target":          plan.Target,
		"capability":      plan.Capability,
	}
	envelope.FailureReason = "query_error"
	envelope.DataStatus = evidence.DataUnknown
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "doesn't exist") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "unknown table") || strings.Contains(lower, "unknown database") {
		envelope.Status = evidence.StatusUnknown
		envelope.ErrorCode = "SOURCE_UNAVAILABLE"
		envelope.FailureReason = "source_unavailable"
	}
	if strings.Contains(lower, "access denied") || strings.Contains(lower, "permission") {
		envelope.Status = evidence.StatusPermissionDenied
		envelope.ErrorCode = "PERMISSION_DENIED"
		envelope.FailureReason = "permission_denied"
	}
	return envelope
}
