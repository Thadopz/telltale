package sqlobserve

import (
	"context"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestUnsupportedDatabaseCapabilityDoesNotOpenAConnection(t *testing.T) {
	observer, err := NewObserverWithAdapter(unsupportedAdapter{engine: EnginePostgreSQL}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}

	envelope, err := observer.Query(context.Background(), QueryRequest{
		Kind:   QuerySlowSQL,
		Target: "card_service_primary",
	})
	if err != nil {
		t.Fatalf("query unsupported adapter: %v", err)
	}
	if envelope.Status != evidence.StatusError || envelope.DataStatus != evidence.DataUnknown {
		t.Fatalf("expected capability failure, got %#v", envelope)
	}
	if envelope.Failure == nil || envelope.Failure.Reason != "capability_not_supported" || envelope.Evidence != nil {
		t.Fatalf("unexpected unsupported capability envelope: %#v", envelope)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok || data["database_engine"] != EnginePostgreSQL || data["target"] != "card_service_primary" {
		t.Fatalf("missing database identity in unsupported result: %#v", envelope.Data)
	}
}

func TestMySQLAdapterMapsGenericSortModes(t *testing.T) {
	adapter := mysqlAdapter{}
	tests := []struct {
		name     string
		sortBy   string
		wantSQL  string
		wantFrom string
	}{
		{name: "latency", sortBy: "latency", wantSQL: "AVG_TIMER_WAIT", wantFrom: "performance_schema"},
		{name: "frequency", sortBy: "frequency", wantSQL: "COUNT_STAR", wantFrom: "performance_schema"},
		{name: "resource", sortBy: "resource", wantSQL: "SUM_ROWS_EXAMINED", wantFrom: "performance_schema"},
		{name: "recent", sortBy: "recent", wantSQL: "FROM mysql.slow_log", wantFrom: "slow_log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := adapter.BuildPlan(QueryRequest{Kind: QuerySlowSQL, Source: "auto", SortBy: tt.sortBy}, DefaultResultBudget())
			if err != nil {
				t.Fatalf("build plan: %v", err)
			}
			if plan.Source != tt.wantFrom || !containsText(plan.SQL, tt.wantSQL) {
				t.Fatalf("unexpected generic mapping: source=%s sql=%s", plan.Source, plan.SQL)
			}
		})
	}
}

func TestObserverAddsDatabaseIdentityToEvidenceScope(t *testing.T) {
	observer, err := NewObserverWithExecutor(func(context.Context, string) (QueryExecutor, error) {
		return fakeExecutor{rows: []map[string]any{{"sql_digest": "select one"}}}, nil
	}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}

	envelope, err := observer.Query(context.Background(), QueryRequest{
		Kind:   QuerySlowSQL,
		Target: "card_service_primary",
		Source: "performance_schema",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("query observer: %v", err)
	}
	if envelope.Status != evidence.StatusSuccess || envelope.Evidence == nil || len(envelope.Evidence.Observations) != 1 {
		t.Fatalf("expected structured evidence, got %#v", envelope)
	}
	scope := envelope.Evidence.Observations[0].Scope.Fields
	if scope["database_engine"] != string(EngineMySQL) || scope["target"] != "card_service_primary" || scope["capability"] != "slow_queries" {
		t.Fatalf("database identity missing from observation scope: %#v", scope)
	}
	if _, ok := envelope.Data.(map[string]any)["database_engine"]; !ok {
		t.Fatalf("database identity missing from result data: %#v", envelope.Data)
	}
}

func TestConfiguredObserverDoesNotUseTelltaleSQLiteAsBusinessDatabase(t *testing.T) {
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("create test config adapter: %v", err)
	}
	adapter.SetContent(`database:
  default:
    type: "sqlite"
    link: "sqlite::@file(data/telltale.db)"
storage:
  sqlite_path: "data/telltale.db"
`)
	previous := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	defer g.Cfg().SetAdapter(previous)

	observer, err := NewConfiguredObserver(context.Background())
	if err != nil {
		t.Fatalf("create configured observer: %v", err)
	}
	envelope, err := observer.Query(context.Background(), QueryRequest{Kind: QueryServerHealth, Target: "default"})
	if err != nil {
		t.Fatalf("query configured observer: %v", err)
	}
	if envelope.Failure == nil || envelope.Failure.Reason != "capability_not_supported" || envelope.Evidence != nil {
		t.Fatalf("expected Telltale SQLite to remain unavailable as a business observer: %#v", envelope)
	}
}

func TestConfiguredDatabaseTargetSelectsMySQLAdapter(t *testing.T) {
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("create test config adapter: %v", err)
	}
	adapter.SetContent(`database:
  default_target: "card_service_primary"
  targets:
    card_service_primary:
      type: "mysql"
      host: "127.0.0.1"
      port: "3306"
      name: "card_service"
      user: "reader"
      pass: "secret"
`)
	previous := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	defer g.Cfg().SetAdapter(previous)

	engine, err := configuredDatabaseEngine(context.Background(), "default")
	if err != nil {
		t.Fatalf("load configured database engine: %v", err)
	}
	if engine != EngineMySQL {
		t.Fatalf("expected mysql engine, got %s", engine)
	}
	selected, err := configuredAdapterForTarget(context.Background(), "default")
	if err != nil {
		t.Fatalf("select configured adapter: %v", err)
	}
	if selected.Engine() != EngineMySQL {
		t.Fatalf("expected mysql adapter, got %s", selected.Engine())
	}
}

func containsText(text string, wanted string) bool {
	return len(wanted) == 0 || strings.Contains(text, wanted)
}
