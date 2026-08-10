package sqlobserve

import (
	"fmt"
	"strings"
)

const defaultSlowSQLLimit = 20

func BuildMySQLQueryPlan(request QueryRequest, budget ResultBudget) (QueryPlan, error) {
	if err := budget.Validate(); err != nil {
		return QueryPlan{}, err
	}
	source := strings.ToLower(strings.TrimSpace(request.Source))
	if source == "auto" {
		source = ""
	}
	sortBy := strings.ToLower(strings.TrimSpace(request.SortBy))
	limit := budget.CLILimit(request.Limit, defaultSlowSQLLimit)

	switch request.Kind {
	case QuerySlowSQL:
		plan, err := buildSlowSQLPlan(source, sortBy, limit, budget.MaxSQLTextChars)
		if err != nil {
			return QueryPlan{}, err
		}
		plan.Window = strings.TrimSpace(request.Window)
		return plan, nil
	case QueryServerHealth:
		return QueryPlan{
			Kind:   QueryServerHealth,
			Source: "global_status",
			Window: strings.TrimSpace(request.Window),
			Limit:  1,
			SQL: `SELECT
  @@GLOBAL.max_connections AS max_connections,
  @@GLOBAL.max_user_connections AS max_user_connections,
  @@GLOBAL.threads_connected AS threads_connected,
  @@GLOBAL.threads_running AS threads_running,
  @@GLOBAL.aborted_connects AS aborted_connects`,
			Projection:      []string{"max_connections", "max_user_connections", "threads_connected", "threads_running", "aborted_connects"},
			ObservationKind: "database_health",
		}, nil
	case QueryActiveQueries:
		plan, err := buildActiveQueriesPlan(limit, budget.MaxSQLTextChars, sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		plan.Window = strings.TrimSpace(request.Window)
		return plan, nil
	case QueryWaitEvents:
		plan, err := buildWaitEventsPlan(limit, sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		plan.Window = strings.TrimSpace(request.Window)
		return plan, nil
	case QueryLocks:
		plan, err := buildLocksPlan(limit, sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		plan.Window = strings.TrimSpace(request.Window)
		return plan, nil
	case QueryRawRead:
		rawLimit := budget.CLILimit(request.Limit, budget.HardMaxRows)
		plan, err := buildRawReadPlan(request.SQL, rawLimit)
		if err != nil {
			return QueryPlan{}, err
		}
		plan.Window = strings.TrimSpace(request.Window)
		return plan, nil
	default:
		return QueryPlan{}, fmt.Errorf("unsupported sql query kind %q", request.Kind)
	}
}

// BuildQueryPlan is kept for existing tests and local integrations. New code
// should build plans through the selected DatabaseAdapter.
func BuildQueryPlan(request QueryRequest, budget ResultBudget) (QueryPlan, error) {
	return BuildMySQLQueryPlan(request, budget)
}

func buildSlowSQLPlan(source string, sortBy string, limit int, maxTextChars int) (QueryPlan, error) {
	if source == "" {
		source = "performance_schema"
	}
	switch source {
	case "performance_schema":
		if sortBy == "" {
			sortBy = "avg_latency"
		}
		orderBy, err := performanceSchemaOrderBy(sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		return QueryPlan{
			Kind:   QuerySlowSQL,
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`SELECT
  SCHEMA_NAME AS db,
  LEFT(DIGEST_TEXT, %d) AS sql_digest,
  COUNT_STAR AS exec_count,
  ROUND(SUM_TIMER_WAIT / 1000000000000, 6) AS total_seconds,
  ROUND(AVG_TIMER_WAIT / 1000000000000, 6) AS avg_seconds,
  ROUND(MAX_TIMER_WAIT / 1000000000000, 6) AS max_seconds,
  SUM_ROWS_EXAMINED AS rows_examined,
  SUM_ROWS_SENT AS rows_sent,
  FIRST_SEEN AS first_seen,
  LAST_SEEN AS last_seen
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST_TEXT IS NOT NULL
ORDER BY %s DESC
LIMIT %d`, maxTextChars, orderBy, limit),
			Projection:      []string{"db", "sql_digest", "exec_count", "total_seconds", "avg_seconds", "max_seconds", "rows_examined", "rows_sent", "first_seen", "last_seen"},
			ObservationKind: "slow_sql",
			LargeTextFields: []string{"sql_digest"},
		}, nil
	case "sys":
		if sortBy == "" {
			sortBy = "avg_latency"
		}
		orderBy, err := sysOrderBy(sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		return QueryPlan{
			Kind:   QuerySlowSQL,
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`SELECT
  db,
  LEFT(query, %d) AS sql_digest,
  exec_count,
  sys.format_time(total_latency) AS total_latency,
  sys.format_time(avg_latency) AS avg_latency,
  sys.format_time(max_latency) AS max_latency,
  rows_examined,
  rows_sent,
  first_seen,
  last_seen
FROM sys.x$statement_analysis
ORDER BY %s DESC
LIMIT %d`, maxTextChars, orderBy, limit),
			Projection:      []string{"db", "sql_digest", "exec_count", "total_latency", "avg_latency", "max_latency", "rows_examined", "rows_sent", "first_seen", "last_seen"},
			ObservationKind: "slow_sql",
			LargeTextFields: []string{"sql_digest"},
		}, nil
	case "slow_log":
		if sortBy == "" {
			sortBy = "query_time"
		}
		orderBy, err := slowLogOrderBy(sortBy)
		if err != nil {
			return QueryPlan{}, err
		}
		return QueryPlan{
			Kind:   QuerySlowSQL,
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`SELECT
  start_time,
  user_host,
  query_time,
  lock_time,
  rows_sent,
  rows_examined,
  db,
  LEFT(sql_text, %d) AS sql_text
FROM mysql.slow_log
ORDER BY %s DESC
LIMIT %d`, maxTextChars, orderBy, limit),
			Projection:      []string{"start_time", "user_host", "query_time", "lock_time", "rows_sent", "rows_examined", "db", "sql_text"},
			ObservationKind: "slow_sql",
			LargeTextFields: []string{"sql_text"},
		}, nil
	default:
		return QueryPlan{}, fmt.Errorf("unsupported slow sql source %q", source)
	}
}

func buildActiveQueriesPlan(limit int, maxTextChars int, sortBy string) (QueryPlan, error) {
	if sortBy == "" {
		sortBy = "duration"
	}
	if sortBy != "duration" && sortBy != "recent" {
		return QueryPlan{}, fmt.Errorf("unsupported active_queries sort_by %q", sortBy)
	}
	return QueryPlan{
		Kind:   QueryActiveQueries,
		Source: "information_schema.processlist",
		Limit:  limit,
		SortBy: sortBy,
		SQL: fmt.Sprintf(`SELECT
  ID AS thread_id,
  USER AS user,
  DB AS db,
  COMMAND AS command,
  TIME AS duration_seconds,
  STATE AS state,
  LEFT(INFO, %d) AS sql_text
FROM information_schema.PROCESSLIST
WHERE COMMAND <> 'Sleep'
ORDER BY TIME DESC
LIMIT %d`, maxTextChars, limit),
		Projection:      []string{"thread_id", "user", "db", "command", "duration_seconds", "state", "sql_text"},
		ObservationKind: "active_query",
		LargeTextFields: []string{"sql_text"},
	}, nil
}

func buildWaitEventsPlan(limit int, sortBy string) (QueryPlan, error) {
	if sortBy == "" {
		sortBy = "total_wait"
	}
	orderBy := "SUM_TIMER_WAIT"
	switch sortBy {
	case "total_wait":
		orderBy = "SUM_TIMER_WAIT"
	case "wait_count":
		orderBy = "COUNT_STAR"
	default:
		return QueryPlan{}, fmt.Errorf("unsupported wait_events sort_by %q", sortBy)
	}
	return QueryPlan{
		Kind:   QueryWaitEvents,
		Source: "performance_schema",
		Limit:  limit,
		SortBy: sortBy,
		SQL: fmt.Sprintf(`SELECT
  EVENT_NAME AS wait_event,
  COUNT_STAR AS wait_count,
  ROUND(SUM_TIMER_WAIT / 1000000000000, 6) AS total_seconds
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0
ORDER BY %s DESC
LIMIT %d`, orderBy, limit),
		Projection:      []string{"wait_event", "wait_count", "total_seconds"},
		ObservationKind: "wait_event",
	}, nil
}

func buildLocksPlan(limit int, sortBy string) (QueryPlan, error) {
	if sortBy != "" && sortBy != "wait_time" {
		return QueryPlan{}, fmt.Errorf("unsupported locks sort_by %q", sortBy)
	}
	return QueryPlan{
		Kind:   QueryLocks,
		Source: "performance_schema",
		Limit:  limit,
		SortBy: sortBy,
		SQL: fmt.Sprintf(`SELECT
  REQUESTING_ENGINE_LOCK_ID AS requesting_lock_id,
  BLOCKING_ENGINE_LOCK_ID AS blocking_lock_id,
  REQUESTING_ENGINE_TRANSACTION_ID AS requesting_transaction_id,
  BLOCKING_ENGINE_TRANSACTION_ID AS blocking_transaction_id
FROM performance_schema.data_lock_waits
LIMIT %d`, limit),
		Projection:      []string{"requesting_lock_id", "blocking_lock_id", "requesting_transaction_id", "blocking_transaction_id"},
		ObservationKind: "lock",
	}, nil
}

func buildRawReadPlan(sql string, limit int) (QueryPlan, error) {
	guarded, err := guardRawReadSQL(sql, limit)
	if err != nil {
		return QueryPlan{}, err
	}
	return QueryPlan{Kind: QueryRawRead, Source: "raw_read", Limit: limit, SQL: guarded}, nil
}

func performanceSchemaOrderBy(sortBy string) (string, error) {
	switch sortBy {
	case "avg_latency":
		return "AVG_TIMER_WAIT", nil
	case "total_latency":
		return "SUM_TIMER_WAIT", nil
	case "rows_examined":
		return "SUM_ROWS_EXAMINED", nil
	case "exec_count":
		return "COUNT_STAR", nil
	default:
		return "", fmt.Errorf("unsupported performance_schema sort_by %q", sortBy)
	}
}

func sysOrderBy(sortBy string) (string, error) {
	switch sortBy {
	case "avg_latency":
		return "avg_latency", nil
	case "total_latency":
		return "total_latency", nil
	case "rows_examined":
		return "rows_examined", nil
	case "exec_count":
		return "exec_count", nil
	default:
		return "", fmt.Errorf("unsupported sys sort_by %q", sortBy)
	}
}

func slowLogOrderBy(sortBy string) (string, error) {
	switch sortBy {
	case "query_time":
		return "query_time", nil
	case "rows_examined":
		return "rows_examined", nil
	case "recent":
		return "start_time", nil
	default:
		return "", fmt.Errorf("unsupported slow_log sort_by %q", sortBy)
	}
}

func guardRawReadSQL(sql string, maxRows int) (string, error) {
	return GuardMySQLReadOnly(sql, maxRows)
}
