package sqlobserve

import (
	"context"
	"strings"
)

type mysqlAdapter struct{}

func newMysqlAdapter() DatabaseAdapter {
	return mysqlAdapter{}
}

func (mysqlAdapter) Engine() DatabaseEngine {
	return EngineMySQL
}

func (mysqlAdapter) Supports(kind QueryKind) bool {
	switch kind {
	case QueryServerHealth, QueryActiveQueries, QuerySlowSQL, QueryWaitEvents, QueryLocks, QueryRawRead:
		return true
	default:
		return false
	}
}

func (mysqlAdapter) BuildPlan(request QueryRequest, budget ResultBudget) (QueryPlan, error) {
	return BuildMySQLQueryPlan(normalizeMySQLRequest(request), budget)
}

func (mysqlAdapter) OpenExecutor(ctx context.Context, target string) (QueryExecutor, error) {
	return openConfiguredMysqlExecutor(ctx, target)
}

type unsupportedAdapter struct {
	engine DatabaseEngine
}

func (adapter unsupportedAdapter) Engine() DatabaseEngine {
	return adapter.engine
}

func (unsupportedAdapter) Supports(QueryKind) bool {
	return false
}

func (adapter unsupportedAdapter) BuildPlan(request QueryRequest, budget ResultBudget) (QueryPlan, error) {
	limit := budget.CLILimit(request.Limit, defaultSlowSQLLimit)
	return QueryPlan{
		Kind:       request.Kind,
		Engine:     adapter.engine,
		Capability: capabilityForQueryKind(request.Kind),
		Target:     request.Target,
		Source:     "unsupported",
		Window:     request.Window,
		SortBy:     request.SortBy,
		Limit:      limit,
	}, nil
}

func (unsupportedAdapter) OpenExecutor(context.Context, string) (QueryExecutor, error) {
	return nil, nil
}

func normalizeMySQLRequest(request QueryRequest) QueryRequest {
	if request.Kind != QuerySlowSQL {
		return request
	}

	request.Source = strings.ToLower(strings.TrimSpace(request.Source))
	request.SortBy = strings.ToLower(strings.TrimSpace(request.SortBy))
	if request.Source == "auto" {
		request.Source = ""
	}

	// These are stable, database-neutral modes exposed to the Agent. The
	// MySQL adapter maps them to its own performance_schema or slow_log fields.
	switch request.SortBy {
	case "latency":
		request.SortBy = "avg_latency"
	case "frequency":
		request.SortBy = "exec_count"
	case "resource":
		request.SortBy = "rows_examined"
	case "recent":
		if request.Source == "" {
			request.Source = "slow_log"
		}
	}
	return request
}
