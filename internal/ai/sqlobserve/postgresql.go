package sqlobserve

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openConfiguredPostgreSQLExecutor(ctx context.Context, target string) (QueryExecutor, error) {
	dsn, err := ConfiguredPostgreSQLDSN(ctx, target)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect postgresql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("load postgresql connection: %w", err)
	}
	return &gormQueryExecutor{db: db, sqlDB: sqlDB}, nil
}

func ConfiguredPostgreSQLDSN(ctx context.Context, target string) (string, error) {
	target = configuredTargetName(ctx, target)
	if !logicalTargetPattern.MatchString(target) {
		return "", fmt.Errorf("invalid logical database target %q", target)
	}
	engine, err := configuredDatabaseEngine(ctx, target)
	if err != nil {
		return "", err
	}
	if engine != EnginePostgreSQL {
		return "", fmt.Errorf("database target %q uses engine %q, not postgresql", target, engine)
	}
	prefix := databaseTargetPrefix(ctx, target)
	user, err := configString(ctx, prefix+".user")
	if err != nil {
		return "", err
	}
	pass, err := configString(ctx, prefix+".pass")
	if err != nil {
		return "", err
	}
	host, err := configString(ctx, prefix+".host")
	if err != nil {
		return "", err
	}
	port, err := configString(ctx, prefix+".port")
	if err != nil {
		return "", err
	}
	database, err := configString(ctx, prefix+".name")
	if err != nil {
		return "", err
	}
	sslMode := strings.ToLower(optionalConfigString(ctx, prefix+".sslmode"))
	if sslMode == "" {
		sslMode = "disable"
	}
	switch sslMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return "", fmt.Errorf("database target %q has invalid sslmode", target)
	}
	dsnURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, pass),
		Host:   net.JoinHostPort(host, port),
		Path:   database,
	}
	query := dsnURL.Query()
	query.Set("sslmode", sslMode)
	dsnURL.RawQuery = query.Encode()
	return dsnURL.String(), nil
}

func buildPostgreSQLHistoryPlan(request QueryRequest, budget ResultBudget) (QueryPlan, error) {
	if err := budget.Validate(); err != nil {
		return QueryPlan{}, err
	}
	limit := budget.CLILimit(request.Limit, defaultSlowSQLLimit)
	sortBy := strings.ToLower(strings.TrimSpace(request.SortBy))
	if sortBy == "" || sortBy == "latency" {
		sortBy = "latency"
	}
	orderBy := "total_exec_ms DESC"
	switch sortBy {
	case "latency":
		orderBy = "total_exec_ms DESC"
	case "frequency":
		orderBy = "exec_count DESC"
	case "resource":
		orderBy = "rows_processed DESC"
	case "recent":
		orderBy = "actual_end DESC"
	default:
		return QueryPlan{}, fmt.Errorf("unsupported postgresql history sort_by %q", sortBy)
	}
	plan := QueryPlan{
		Kind:            QuerySlowSQL,
		Engine:          EnginePostgreSQL,
		Source:          "pg_stat_statements_history",
		SortBy:          sortBy,
		Limit:           limit,
		LocalHistory:    true,
		TimeRange:       request.ResolvedTimeRange,
		Projection:      []string{"db", "statement_fingerprint", "sql_digest", "exec_count", "total_exec_ms", "avg_exec_ms", "rows_processed", "actual_start", "actual_end"},
		ObservationKind: "slow_sql",
		LargeTextFields: []string{"sql_digest"},
	}
	if plan.TimeRange == nil {
		return plan, nil
	}
	plan.SQL = fmt.Sprintf(`SELECT
  database_name AS db,
  statement_fingerprint,
  MAX(statement_preview) AS sql_digest,
  SUM(calls_delta) AS exec_count,
  ROUND(SUM(total_exec_ms_delta), 6) AS total_exec_ms,
  ROUND(CASE WHEN SUM(calls_delta) > 0 THEN SUM(total_exec_ms_delta) / SUM(calls_delta) ELSE 0 END, 6) AS avg_exec_ms,
  SUM(rows_delta) AS rows_processed,
  MIN(bucket_start) AS actual_start,
  MAX(bucket_end) AS actual_end
FROM database_observation_samples
WHERE target = ? AND kind = 'slow_sql' AND bucket_end > ? AND bucket_start < ?
GROUP BY database_name, statement_fingerprint
HAVING SUM(calls_delta) > 0 OR SUM(total_exec_ms_delta) > 0 OR SUM(rows_delta) > 0
ORDER BY %s
LIMIT %d`, orderBy, limit)
	plan.Args = []any{request.Target, plan.TimeRange.Start.UTC(), plan.TimeRange.End.UTC()}
	return plan, nil
}
