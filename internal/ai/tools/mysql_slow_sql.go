package tools

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/sqlobserve"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	defaultSlowSQLLimit = 20
	maxSlowSQLLimit     = 100
)

type MysqlSlowSQLInput struct {
	Target    string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to default; never provide a DSN or server credentials"`
	Source    string `json:"source,omitempty" jsonschema:"description=Slow SQL source: performance_schema, sys, slow_log, or auto. Defaults to performance_schema"`
	StartTime string `json:"start_time,omitempty" jsonschema:"description=Inclusive RFC3339 start time; must be used together with end_time and not with window"`
	EndTime   string `json:"end_time,omitempty" jsonschema:"description=Exclusive RFC3339 end time; must be used together with start_time and not with window"`
	Window    string `json:"window,omitempty" jsonschema:"description=Relative window such as 5m or 30m; anchored to the target database clock"`
	SortBy    string `json:"sort_by,omitempty" jsonschema:"description=Sort mode: avg_latency, total_latency, rows_examined, exec_count, query_time, or recent. Defaults depend on source"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum rows to return. Values are clamped to 1..100; default is 20"`
}

// DBSlowQueriesInput is the database-agent input. It deliberately omits the
// adapter-specific source field; the configured adapter chooses its source.
type DBSlowQueriesInput struct {
	Target    string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to the configured default target"`
	StartTime string `json:"start_time,omitempty" jsonschema:"description=Inclusive RFC3339 start time; must be used together with end_time and not with window"`
	EndTime   string `json:"end_time,omitempty" jsonschema:"description=Exclusive RFC3339 end time; must be used together with start_time and not with window"`
	Window    string `json:"window,omitempty" jsonschema:"description=Relative window such as 5m or 30m; defaults to the last 30m using the target database clock"`
	SortBy    string `json:"sort_by,omitempty" jsonschema:"description=Generic sort mode such as latency, frequency, resource, or recent"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum rows to return; the server applies the configured limit"`
}

type mysqlSlowSQLQuery struct {
	Source string
	SortBy string
	Limit  int
	SQL    string
}

func NewDBSlowQueriesTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"db_slow_queries",
		"Find bounded top slow-query records for the configured business database using its supported read-only observation source.",
		func(ctx context.Context, input *DBSlowQueriesInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &DBSlowQueriesInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:             sqlobserve.QuerySlowSQL,
				Target:           input.Target,
				Source:           "auto",
				StartTime:        input.StartTime,
				EndTime:          input.EndTime,
				Window:           input.Window,
				DefaultTimeRange: true,
				SortBy:           input.SortBy,
				Limit:            budget.AgentLimit(input.Limit, defaultSlowSQLLimit),
				MaxBytes:         budget.AgentMaxBytes,
			})
		},
	)
}

func NewMysqlSlowSQLTool() (tool.InvokableTool, error) {
	t, err := utils.InferOptionableTool(
		"mysql_slow_sql",
		"Find slow MySQL statements using fixed read-only queries against performance_schema, sys.statement_analysis, or mysql.slow_log. Use this when investigating database latency, slow API responses, query timeout, high rows examined, or SQL performance issues.",
		func(ctx context.Context, input *MysqlSlowSQLInput, opts ...tool.Option) (string, error) {
			if input == nil {
				input = &MysqlSlowSQLInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			observer, err := sqlobserve.NewConfiguredObserver(ctx)
			if err != nil {
				return marshalToolError(err, "load mysql observation configuration failed"), nil
			}
			envelope, queryErr := observer.Query(ctx, sqlobserve.QueryRequest{
				Kind:             sqlobserve.QuerySlowSQL,
				Target:           input.Target,
				Source:           input.Source,
				StartTime:        input.StartTime,
				EndTime:          input.EndTime,
				Window:           input.Window,
				DefaultTimeRange: true,
				SortBy:           input.SortBy,
				Limit:            budget.AgentLimit(input.Limit, defaultSlowSQLLimit),
				MaxBytes:         budget.AgentMaxBytes,
			})
			if queryErr != nil {
				return marshalToolError(queryErr, "execute mysql slow sql query failed"), nil
			}
			return evidence.Marshal(envelope), nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func buildMysqlSlowSQLQuery(input *MysqlSlowSQLInput) (mysqlSlowSQLQuery, error) {
	source := "performance_schema"
	sortBy := ""
	limit := defaultSlowSQLLimit
	if input != nil {
		source = strings.ToLower(strings.TrimSpace(input.Source))
		sortBy = strings.ToLower(strings.TrimSpace(input.SortBy))
		if input.Limit != 0 {
			limit = input.Limit
		}
	}
	if source == "" || source == "auto" {
		source = "performance_schema"
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxSlowSQLLimit {
		limit = maxSlowSQLLimit
	}

	switch source {
	case "performance_schema":
		if sortBy == "" {
			sortBy = "avg_latency"
		}
		orderBy, err := performanceSchemaSlowSQLOrderBy(sortBy)
		if err != nil {
			return mysqlSlowSQLQuery{}, err
		}
		return mysqlSlowSQLQuery{
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`
SELECT
  SCHEMA_NAME AS db,
  DIGEST_TEXT AS sql_digest,
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
LIMIT %d`, orderBy, limit),
		}, nil
	case "sys":
		if sortBy == "" {
			sortBy = "avg_latency"
		}
		orderBy, err := sysSlowSQLOrderBy(sortBy)
		if err != nil {
			return mysqlSlowSQLQuery{}, err
		}
		return mysqlSlowSQLQuery{
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`
SELECT
  db,
  query AS sql_digest,
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
LIMIT %d`, orderBy, limit),
		}, nil
	case "slow_log":
		if sortBy == "" {
			sortBy = "query_time"
		}
		orderBy, err := slowLogOrderBy(sortBy)
		if err != nil {
			return mysqlSlowSQLQuery{}, err
		}
		return mysqlSlowSQLQuery{
			Source: source,
			SortBy: sortBy,
			Limit:  limit,
			SQL: fmt.Sprintf(`
SELECT
  start_time,
  user_host,
  query_time,
  lock_time,
  rows_sent,
  rows_examined,
  db,
  sql_text
FROM mysql.slow_log
ORDER BY %s DESC
LIMIT %d`, orderBy, limit),
		}, nil
	default:
		return mysqlSlowSQLQuery{}, fmt.Errorf("unsupported slow sql source %q", source)
	}
}

func performanceSchemaSlowSQLOrderBy(sortBy string) (string, error) {
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

func sysSlowSQLOrderBy(sortBy string) (string, error) {
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
