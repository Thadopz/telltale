package sqlobserve

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestResolveTimeRangeUsesDatabaseClockAndUTC(t *testing.T) {
	databaseNow := time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	rangeValue, err := resolveTimeRange(QueryRequest{Kind: QuerySlowSQL, DefaultTimeRange: true}, databaseNow)
	if err != nil {
		t.Fatalf("resolve default range: %v", err)
	}
	if rangeValue.Source != TimeRangeDatabaseDefault || rangeValue.Window() != 30*time.Minute {
		t.Fatalf("unexpected default range: %#v", rangeValue)
	}
	if rangeValue.End.Location() != time.UTC || !rangeValue.End.Equal(databaseNow) {
		t.Fatalf("range was not normalized to the database clock in UTC: %#v", rangeValue)
	}

	explicit, err := resolveTimeRange(QueryRequest{
		StartTime: "2026-08-11T08:00:00+08:00",
		EndTime:   "2026-08-11T09:00:00+08:00",
	}, databaseNow)
	if err != nil {
		t.Fatalf("resolve explicit range: %v", err)
	}
	if got := explicit.Start.Format(time.RFC3339); got != "2026-08-11T00:00:00Z" {
		t.Fatalf("unexpected UTC start: %s", got)
	}
}

func TestResolveTimeRangeRejectsUnsafeOrUnboundedInputs(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	tests := []QueryRequest{
		{StartTime: now.Add(-time.Hour).Format(time.RFC3339)},
		{StartTime: now.Add(-time.Hour).Format(time.RFC3339), EndTime: now.Format(time.RFC3339), Window: "1h"},
		{StartTime: "2026-08-11T00:00:00Z' OR 1=1 --", EndTime: now.Format(time.RFC3339)},
		{StartTime: now.Add(-25 * time.Hour).Format(time.RFC3339), EndTime: now.Format(time.RFC3339)},
		{StartTime: now.Format(time.RFC3339), EndTime: now.Format(time.RFC3339)},
		{StartTime: now.Format(time.RFC3339), EndTime: now.Add(10 * time.Minute).Format(time.RFC3339)},
	}
	for index, request := range tests {
		if _, err := resolveTimeRange(request, now); err == nil {
			t.Fatalf("case %d: expected invalid time range", index)
		}
	}
}

func TestMySQLTimeRangeUsesBoundParametersAndSlowLog(t *testing.T) {
	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	plan, err := mysqlAdapter{}.BuildPlan(QueryRequest{
		Kind:              QuerySlowSQL,
		Source:            "performance_schema",
		SortBy:            "latency",
		ResolvedTimeRange: &TimeRange{Start: start, End: end, Source: TimeRangeExplicit},
	}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build ranged MySQL plan: %v", err)
	}
	if plan.Source != "slow_log" || !strings.Contains(plan.SQL, "start_time >= ? AND start_time < ?") {
		t.Fatalf("expected exact slow_log range query: source=%s sql=%s", plan.Source, plan.SQL)
	}
	if strings.Contains(plan.SQL, start.Format(time.RFC3339)) || len(plan.Args) != 2 {
		t.Fatalf("time values must remain bound arguments: sql=%s args=%#v", plan.SQL, plan.Args)
	}
}

func TestTrustedIncidentRangeOverridesToolArgumentsAndAllowsOneCall(t *testing.T) {
	executor := &clockExecutor{now: time.Now().UTC().Add(-time.Second).Truncate(time.Second)}
	observer, err := NewObserverWithExecutor(func(context.Context, string) (QueryExecutor, error) {
		return executor, nil
	}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}
	start := executor.now.Add(-5 * time.Minute)
	ctx := WithTrustedTimeRange(context.Background(), "card_service_primary", start, executor.now)
	envelope, err := observer.Query(ctx, QueryRequest{
		Kind:      QuerySlowSQL,
		Target:    "attacker_database",
		StartTime: "2020-01-01T00:00:00Z",
		EndTime:   "2020-01-01T01:00:00Z",
	})
	if err != nil || envelope.Status != evidence.StatusSuccess {
		t.Fatalf("run trusted incident query: envelope=%#v err=%v", envelope, err)
	}
	if len(executor.args) != 2 || !executor.args[0].(time.Time).Equal(start) || !executor.args[1].(time.Time).Equal(executor.now) {
		t.Fatalf("trusted incident range was not used: %#v", executor.args)
	}
	if target := envelope.Evidence.Observations[0].Scope.Fields["target"]; target != "card_service_primary" {
		t.Fatalf("trusted target was not used: %s", target)
	}

	second, err := observer.Query(ctx, QueryRequest{Kind: QuerySlowSQL})
	if err != nil || second.Failure == nil || second.Failure.Code != "INVALID_TIME_RANGE" || second.Evidence != nil {
		t.Fatalf("second automatic database call should be rejected: %#v err=%v", second, err)
	}
}

type clockExecutor struct {
	now  time.Time
	args []any
}

func (executor *clockExecutor) Query(_ context.Context, _ string, args ...any) ([]map[string]any, error) {
	executor.args = append([]any(nil), args...)
	return []map[string]any{{
		"start_time": executor.now.Add(-time.Minute),
		"query_time": "00:00:01",
		"db":         "card_service",
		"sql_text":   "SELECT card_id FROM cards WHERE card_id = ?",
	}}, nil
}

func (executor *clockExecutor) CurrentTime(context.Context) (time.Time, error) {
	return executor.now, nil
}

func (*clockExecutor) Close() error { return nil }
