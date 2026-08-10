package sqlobserve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestReduceRowsAppliesRowAndByteLimits(t *testing.T) {
	rows := []map[string]any{
		{"id": 1, "sql_text": strings.Repeat("select ", 100)},
		{"id": 2, "sql_text": "select two"},
		{"id": 3, "sql_text": "select three"},
	}

	reduced, metadata, err := ReduceRows(rows, 2, 180, 20)
	if err != nil {
		t.Fatalf("reduce rows: %v", err)
	}
	if len(reduced) != 2 {
		t.Fatalf("expected two rows, got %d", len(reduced))
	}
	if !metadata.Truncated || !strings.Contains(metadata.TruncationReason, "max_rows") {
		t.Fatalf("expected row truncation metadata, got %#v", metadata)
	}
	if len(reduced[0]["sql_text"].(string)) > 20 {
		t.Fatalf("expected sql text to be clipped, got %d characters", len(reduced[0]["sql_text"].(string)))
	}
	if metadata.ReturnedBytes > metadata.MaxBytes {
		t.Fatalf("returned bytes exceed budget: %#v", metadata)
	}
}

func TestReduceRowsReportsSingleRowBudgetFailure(t *testing.T) {
	rows := []map[string]any{{"message": strings.Repeat("x", 100)}}
	_, _, err := ReduceRows(rows, 10, 20, 1000)
	if err == nil || !strings.Contains(err.Error(), "result_row_exceeds_budget") {
		t.Fatalf("expected a single-row budget error, got %v", err)
	}
}

func TestObserverReturnsPartialStructuredEvidence(t *testing.T) {
	budget := DefaultResultBudget()
	budget.AgentMaxBytes = 4096
	observer, err := NewObserverWithExecutor(func(context.Context, string) (QueryExecutor, error) {
		return fakeExecutor{rows: []map[string]any{
			{"sql_digest": "select one", "avg_seconds": 3.2},
			{"sql_digest": "select two", "avg_seconds": 2.1},
		}}, nil
	}, budget)
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}

	envelope, err := observer.Query(context.Background(), QueryRequest{
		Kind:     QuerySlowSQL,
		Source:   "performance_schema",
		Limit:    1,
		MaxBytes: 4096,
	})
	if err != nil {
		t.Fatalf("query observer: %v", err)
	}
	if envelope.Status != evidence.StatusSuccess || len(envelope.Observations) != 1 {
		t.Fatalf("expected structured success, got %#v", envelope)
	}
	if !envelope.ResultMetadata.Truncated || envelope.DataStatus != evidence.DataPartial {
		t.Fatalf("expected partial result metadata, got %#v", envelope)
	}
}

func TestBuildQueryPlanRejectsRawWrite(t *testing.T) {
	_, err := BuildQueryPlan(QueryRequest{Kind: QueryRawRead, SQL: "DELETE FROM orders"}, DefaultResultBudget())
	if err == nil {
		t.Fatal("expected raw write to be rejected")
	}
}

func TestBuildQueryPlanUsesFixedProjectionAndHardLimit(t *testing.T) {
	budget := DefaultResultBudget()
	plan, err := BuildQueryPlan(QueryRequest{
		Kind:   QuerySlowSQL,
		Source: "performance_schema",
		SortBy: "avg_latency",
		Limit:  10000,
	}, budget)
	if err != nil {
		t.Fatalf("build slow sql plan: %v", err)
	}
	if plan.Limit != budget.HardMaxRows {
		t.Fatalf("expected hard limit %d, got %d", budget.HardMaxRows, plan.Limit)
	}
	if strings.Contains(strings.ToLower(plan.SQL), "select *") {
		t.Fatalf("fixed plan must not use select *: %s", plan.SQL)
	}
	if !strings.Contains(plan.SQL, "LEFT(DIGEST_TEXT") {
		t.Fatalf("expected SQL-level digest clipping: %s", plan.SQL)
	}
	if _, err := BuildQueryPlan(QueryRequest{Kind: QuerySlowSQL, SortBy: "arbitrary_expression"}, budget); err == nil {
		t.Fatal("expected unsupported sort field to be rejected")
	}
}

func TestObserverProjectsUnexpectedColumns(t *testing.T) {
	observer, err := NewObserverWithExecutor(func(context.Context, string) (QueryExecutor, error) {
		return fakeExecutor{rows: []map[string]any{{
			"sql_digest":  "select one",
			"avg_seconds": 1.2,
			"password":    "must not reach the result",
		}}}, nil
	}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}
	envelope, err := observer.Query(context.Background(), QueryRequest{Kind: QuerySlowSQL, Limit: 1})
	if err != nil {
		t.Fatalf("query observer: %v", err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map data, got %#v", envelope.Data)
	}
	rows, ok := data["rows"].([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected one projected row, got %#v", data["rows"])
	}
	if _, exists := rows[0]["password"]; exists {
		t.Fatal("unexpected column escaped the fixed projection")
	}
}

func TestObserverDistinguishesEmptyPermissionAndUnavailableResults(t *testing.T) {
	tests := []struct {
		name        string
		executorErr error
		wantStatus  string
		wantReason  string
		wantData    string
	}{
		{name: "empty", wantStatus: evidence.StatusSuccess, wantReason: "empty_result", wantData: evidence.DataEmpty},
		{name: "permission", executorErr: errors.New("access denied for performance_schema"), wantStatus: evidence.StatusPermissionDenied, wantReason: "permission_denied", wantData: evidence.DataUnknown},
		{name: "unavailable", executorErr: errors.New("table performance_schema.missing does not exist"), wantStatus: evidence.StatusUnknown, wantReason: "source_unavailable", wantData: evidence.DataUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer, err := NewObserverWithExecutor(func(context.Context, string) (QueryExecutor, error) {
				return fakeExecutor{err: test.executorErr}, nil
			}, DefaultResultBudget())
			if err != nil {
				t.Fatalf("create observer: %v", err)
			}
			envelope, err := observer.Query(context.Background(), QueryRequest{Kind: QuerySlowSQL})
			if err != nil {
				t.Fatalf("query observer: %v", err)
			}
			if envelope.Status != test.wantStatus || envelope.FailureReason != test.wantReason || envelope.DataStatus != test.wantData {
				t.Fatalf("unexpected result: %#v", envelope)
			}
		})
	}
}

func TestRawReadPlanAddsBoundedLimit(t *testing.T) {
	plan, err := BuildQueryPlan(QueryRequest{Kind: QueryRawRead, SQL: "SELECT id FROM orders LIMIT 10000"}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build raw read plan: %v", err)
	}
	if !strings.Contains(plan.SQL, "LIMIT 100") {
		t.Fatalf("expected hard limit in raw read, got %s", plan.SQL)
	}
}

func TestConfiguredMysqlDSNRejectsConnectionStringAsTarget(t *testing.T) {
	if _, err := ConfiguredMysqlDSN(context.Background(), "user:pass@tcp(host:3306)/db"); err == nil {
		t.Fatal("expected a DSN to be rejected as a logical target")
	}
}

type fakeExecutor struct {
	rows []map[string]any
	err  error
}

func (executor fakeExecutor) Query(context.Context, string) ([]map[string]any, error) {
	return executor.rows, executor.err
}

func (fakeExecutor) Close() error {
	return nil
}
