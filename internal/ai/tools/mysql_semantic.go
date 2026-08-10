package tools

import (
	"context"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/sqlobserve"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// These inputs deliberately contain logical observation parameters only. They
// do not expose SQL, DSNs, hosts, usernames, or passwords to the model.
type MysqlServerHealthInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to default"`
	Window string `json:"window,omitempty" jsonschema:"description=Optional observation window for correlating this result"`
}

type MysqlActiveQueriesInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to default"`
	Window string `json:"window,omitempty" jsonschema:"description=Optional observation window such as 5m or 30m"`
	SortBy string `json:"sort_by,omitempty" jsonschema:"description=Sort mode: duration or recent"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum rows to return; the server applies the configured limit"`
}

type MysqlWaitEventsInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to default"`
	Window string `json:"window,omitempty" jsonschema:"description=Optional observation window such as 5m or 30m"`
	SortBy string `json:"sort_by,omitempty" jsonschema:"description=Sort mode: total_wait or wait_count"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum rows to return; the server applies the configured limit"`
}

type MysqlLocksInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Logical database target name. Defaults to default"`
	Window string `json:"window,omitempty" jsonschema:"description=Optional observation window such as 5m or 30m"`
	SortBy string `json:"sort_by,omitempty" jsonschema:"description=Sort mode: wait_time"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum rows to return; the server applies the configured limit"`
}

type DBServerHealthInput = MysqlServerHealthInput
type DBActiveQueriesInput = MysqlActiveQueriesInput
type DBWaitEventsInput = MysqlWaitEventsInput
type DBLocksInput = MysqlLocksInput

func NewDBServerHealthTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"db_server_health",
		"Inspect fixed health counters for the configured business database. This does not represent an application connection-pool metric.",
		func(ctx context.Context, input *DBServerHealthInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &DBServerHealthInput{}
			}
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:   sqlobserve.QueryServerHealth,
				Target: input.Target,
				Window: input.Window,
			})
		},
	)
}

func NewDBActiveQueriesTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"db_active_queries",
		"Inspect currently active queries in the configured business database with a bounded, fixed projection.",
		func(ctx context.Context, input *DBActiveQueriesInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &DBActiveQueriesInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryActiveQueries,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func NewDBWaitEventsTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"db_wait_events",
		"Inspect aggregated wait events for the configured business database to distinguish database waits from application-side saturation.",
		func(ctx context.Context, input *DBWaitEventsInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &DBWaitEventsInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryWaitEvents,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func NewDBLocksTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"db_locks",
		"Inspect current lock waits in the configured business database when lock contention is a distinct hypothesis.",
		func(ctx context.Context, input *DBLocksInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &DBLocksInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryLocks,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func NewMysqlServerHealthTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"mysql_server_health",
		"Inspect fixed MySQL server health counters. Use this for database capacity and active connection evidence; it does not represent an application connection-pool metric.",
		func(ctx context.Context, input *MysqlServerHealthInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &MysqlServerHealthInput{}
			}
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:   sqlobserve.QueryServerHealth,
				Target: input.Target,
				Window: input.Window,
			})
		},
	)
}

func NewMysqlActiveQueriesTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"mysql_active_queries",
		"Inspect currently active MySQL queries with a bounded, fixed projection. Use this to investigate live database work and long-running statements.",
		func(ctx context.Context, input *MysqlActiveQueriesInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &MysqlActiveQueriesInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryActiveQueries,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func NewMysqlWaitEventsTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"mysql_wait_events",
		"Inspect aggregated MySQL wait events with a bounded fixed projection. Use this to distinguish database waits from application-side saturation.",
		func(ctx context.Context, input *MysqlWaitEventsInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &MysqlWaitEventsInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryWaitEvents,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func NewMysqlLocksTool() (tool.InvokableTool, error) {
	return utils.InferOptionableTool(
		"mysql_locks",
		"Inspect current MySQL lock waits using a bounded fixed projection. Use this when lock contention is a distinct hypothesis.",
		func(ctx context.Context, input *MysqlLocksInput, _ ...tool.Option) (string, error) {
			if input == nil {
				input = &MysqlLocksInput{}
			}
			budget := sqlobserve.LoadResultBudget(ctx)
			return runConfiguredSQLObservation(ctx, sqlobserve.QueryRequest{
				Kind:     sqlobserve.QueryLocks,
				Target:   input.Target,
				Window:   input.Window,
				SortBy:   input.SortBy,
				Limit:    budget.AgentLimit(input.Limit, 20),
				MaxBytes: budget.AgentMaxBytes,
			})
		},
	)
}

func runConfiguredSQLObservation(ctx context.Context, request sqlobserve.QueryRequest) (string, error) {
	observer, err := sqlobserve.NewConfiguredObserver(ctx)
	if err != nil {
		return evidence.Marshal(evidence.NewError(err, "load database observation configuration failed")), nil
	}
	envelope, err := observer.Query(ctx, request)
	if err != nil {
		return evidence.Marshal(evidence.NewError(err, "execute database observation query failed")), nil
	}
	return evidence.Marshal(envelope), nil
}
