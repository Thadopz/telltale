package sqlobserve

import (
	"context"
	"fmt"
)

type QueryKind string

const (
	QuerySlowSQL       QueryKind = "slow_sql"
	QueryServerHealth  QueryKind = "server_health"
	QueryActiveQueries QueryKind = "active_queries"
	QueryWaitEvents    QueryKind = "wait_events"
	QueryLocks         QueryKind = "locks"
	QueryRawRead       QueryKind = "raw_read"
)

type DatabaseEngine string

const (
	EngineMySQL      DatabaseEngine = "mysql"
	EnginePostgreSQL DatabaseEngine = "postgresql"
	EngineSQLite     DatabaseEngine = "sqlite"
	EngineUnknown    DatabaseEngine = "unknown"
)

type QueryRequest struct {
	Kind     QueryKind
	Target   string
	Source   string
	Window   string
	SortBy   string
	Limit    int
	MaxBytes int
	SQL      string
}

type ResultBudget struct {
	AgentMaxBytes   int
	AgentMaxRows    int
	HardMaxRows     int
	CLIMaxBytes     int
	MaxSQLTextChars int
	MaxObservations int
}

const (
	defaultAgentMaxBytes   = 64 * 1024
	defaultAgentMaxRows    = 50
	defaultHardMaxRows     = 100
	defaultCLIMaxBytes     = 1024 * 1024
	defaultMaxSQLTextChars = 1000
	defaultMaxObservations = 32
)

type QueryPlan struct {
	Kind            QueryKind
	Engine          DatabaseEngine
	Capability      string
	Target          string
	Source          string
	Window          string
	SortBy          string
	Limit           int
	SQL             string
	Projection      []string
	ObservationKind string
	LargeTextFields []string
}

// DatabaseAdapter contains the database-specific part of a bounded
// observation query. Agent-facing tools use the common QueryKind values and
// never need to know which adapter produced the result.
type DatabaseAdapter interface {
	Engine() DatabaseEngine
	Supports(QueryKind) bool
	BuildPlan(QueryRequest, ResultBudget) (QueryPlan, error)
	OpenExecutor(context.Context, string) (QueryExecutor, error)
}

type QueryExecutor interface {
	Query(context.Context, string) ([]map[string]any, error)
	Close() error
}

type ExecutorFactory func(context.Context, string) (QueryExecutor, error)

type Observer struct {
	Budget         ResultBudget
	OpenExecutor   ExecutorFactory
	Adapter        DatabaseAdapter
	ResolveAdapter func(context.Context, string) (DatabaseAdapter, error)
}

func DefaultResultBudget() ResultBudget {
	return ResultBudget{
		AgentMaxBytes:   defaultAgentMaxBytes,
		AgentMaxRows:    defaultAgentMaxRows,
		HardMaxRows:     defaultHardMaxRows,
		CLIMaxBytes:     defaultCLIMaxBytes,
		MaxSQLTextChars: defaultMaxSQLTextChars,
		MaxObservations: defaultMaxObservations,
	}
}

func (budget ResultBudget) Validate() error {
	if budget.AgentMaxBytes <= 0 {
		return fmt.Errorf("agent_max_bytes must be positive")
	}
	if budget.AgentMaxRows <= 0 {
		return fmt.Errorf("agent_max_rows must be positive")
	}
	if budget.HardMaxRows <= 0 {
		return fmt.Errorf("hard_max_rows must be positive")
	}
	if budget.AgentMaxRows > budget.HardMaxRows {
		return fmt.Errorf("agent_max_rows cannot exceed hard_max_rows")
	}
	if budget.CLIMaxBytes <= 0 {
		return fmt.Errorf("cli_max_bytes must be positive")
	}
	if budget.MaxSQLTextChars <= 0 {
		return fmt.Errorf("max_sql_text_chars must be positive")
	}
	if budget.MaxObservations <= 0 {
		return fmt.Errorf("max_observations must be positive")
	}
	return nil
}

func (budget ResultBudget) AgentLimit(requested int, defaultLimit int) int {
	return clampLimit(requested, defaultLimit, budget.AgentMaxRows, budget.HardMaxRows)
}

func (budget ResultBudget) CLILimit(requested int, defaultLimit int) int {
	return clampLimit(requested, defaultLimit, budget.HardMaxRows, budget.HardMaxRows)
}

func clampLimit(requested int, defaultLimit int, preferredMax int, hardMax int) int {
	if hardMax <= 0 {
		hardMax = defaultHardMaxRows
	}
	if preferredMax <= 0 || preferredMax > hardMax {
		preferredMax = hardMax
	}
	limit := requested
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit <= 0 {
		limit = preferredMax
	}
	if limit > preferredMax {
		limit = preferredMax
	}
	if limit > hardMax {
		limit = hardMax
	}
	return limit
}
