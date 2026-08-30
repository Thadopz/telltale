package sqlobserve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "telltale-sqlobserve-")
	if err != nil {
		panic(err)
	}
	if err = os.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(directory, "history.db")); err != nil {
		panic(err)
	}
	if err = storage.Initialize(context.Background()); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = g.DB().Close(context.Background())
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestLoadObservationHistoryConfigSelectsExplicitPostgreSQLTargets(t *testing.T) {
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("create config adapter: %v", err)
	}
	adapter.SetContent(`database:
  observation_history:
    enabled: true
    sample_interval_seconds: 30
    retention_hours: 24
    max_statements_per_sample: 75
  targets:
    card_service_primary:
      type: postgresql
      history_enabled: true
    reporting:
      type: postgresql
      history_enabled: false
`)
	previous := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	defer g.Cfg().SetAdapter(previous)

	config, err := LoadObservationHistoryConfig(context.Background())
	if err != nil {
		t.Fatalf("load observation history config: %v", err)
	}
	if !config.Enabled || config.SampleInterval != 30*time.Second || config.Retention != 24*time.Hour {
		t.Fatalf("unexpected history config: %#v", config)
	}
	if config.MaxStatementsPerSample != defaultHistoryMaxStatements {
		t.Fatalf("configured sample count should be capped at %d, got %d", defaultHistoryMaxStatements, config.MaxStatementsPerSample)
	}
	if len(config.Targets) != 1 || config.Targets[0].Name != "card_service_primary" {
		t.Fatalf("unexpected history targets: %#v", config.Targets)
	}
}

func TestPostgreSQLSamplerBuildsBaselineDeltaAndReset(t *testing.T) {
	ctx := context.Background()
	target := HistoryTarget{Name: "card_service_primary", Engine: EnginePostgreSQL}
	sampler := &HistorySampler{config: ObservationHistoryConfig{Retention: 48 * time.Hour}, owner: "test-owner"}
	baseTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	baseline := pgStatementSnapshot{Fingerprint: "card_service:42", Database: "card_service", Preview: "SELECT pg_sleep(?)", Calls: 10, TotalExecMS: 1000, Rows: 10, ObservedAt: baseTime}
	if err := sampler.persistSnapshots(ctx, target, []pgStatementSnapshot{baseline}); err != nil {
		t.Fatalf("persist baseline: %v", err)
	}
	if count, _ := g.DB().Model("database_observation_samples").Count(); count != 0 {
		t.Fatalf("first sample must only establish a baseline, got %d deltas", count)
	}

	next := baseline
	next.Calls = 12
	next.TotalExecMS = 1600
	next.Rows = 14
	next.ObservedAt = baseTime.Add(time.Minute)
	if err := sampler.persistSnapshots(ctx, target, []pgStatementSnapshot{next}); err != nil {
		t.Fatalf("persist delta: %v", err)
	}
	var samples []struct {
		CallsDelta       int64   `json:"calls_delta"`
		TotalExecMSDelta float64 `json:"total_exec_ms_delta"`
		RowsDelta        int64   `json:"rows_delta"`
	}
	if err := g.DB().Model("database_observation_samples").Scan(&samples); err != nil {
		t.Fatalf("read deltas: %v", err)
	}
	if len(samples) != 1 || samples[0].CallsDelta != 2 || samples[0].TotalExecMSDelta != 600 || samples[0].RowsDelta != 4 {
		t.Fatalf("unexpected sampled delta: %#v", samples)
	}
	unchanged := next
	unchanged.ObservedAt = baseTime.Add(90 * time.Second)
	if err := sampler.persistSnapshots(ctx, target, []pgStatementSnapshot{unchanged}); err != nil {
		t.Fatalf("persist unchanged sample: %v", err)
	}
	if count, _ := g.DB().Model("database_observation_samples").Count(); count != 1 {
		t.Fatalf("unchanged counters must not create evidence, got %d samples", count)
	}

	reset := next
	reset.Calls = 1
	reset.TotalExecMS = 10
	reset.Rows = 0
	reset.ObservedAt = baseTime.Add(2 * time.Minute)
	if err := sampler.persistSnapshots(ctx, target, []pgStatementSnapshot{reset}); err != nil {
		t.Fatalf("persist reset baseline: %v", err)
	}
	if count, _ := g.DB().Model("database_observation_samples").Count(); count != 1 {
		t.Fatalf("counter reset must not create a negative delta, got %d samples", count)
	}

	statsReset := reset
	statsReset.Calls = 100
	statsReset.TotalExecMS = 10000
	statsReset.Rows = 100
	statsReset.ObservedAt = baseTime.Add(3 * time.Minute)
	statsReset.StatsReset = baseTime.Add(150 * time.Second)
	if err := sampler.persistSnapshots(ctx, target, []pgStatementSnapshot{statsReset}); err != nil {
		t.Fatalf("persist statistics reset: %v", err)
	}
	if count, _ := g.DB().Model("database_observation_samples").Count(); count != 1 {
		t.Fatalf("statistics reset must rebuild the baseline, got %d samples", count)
	}
}

func TestPostgreSQLHistoryQueryReportsPartialCoverage(t *testing.T) {
	ctx := context.Background()
	if err := recordHistoryTargetStatus(ctx, HistoryTarget{Name: "card_service_primary", Engine: EnginePostgreSQL}, nil); err != nil {
		t.Fatalf("record sampler status: %v", err)
	}
	baseTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	rangeValue := &TimeRange{Start: baseTime.Add(-time.Minute), End: baseTime.Add(2 * time.Minute), Source: TimeRangeExplicit, Semantics: "sampled_delta"}
	plan, err := buildPostgreSQLHistoryPlan(QueryRequest{Kind: QuerySlowSQL, Target: "card_service_primary", ResolvedTimeRange: rangeValue}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build history plan: %v", err)
	}
	plan.Target = "card_service_primary"
	rows, err := queryLocalHistory(ctx, &plan)
	if err != nil {
		t.Fatalf("query sampled history: %v", err)
	}
	if len(rows) != 1 || plan.DataStatus != "partial" {
		t.Fatalf("expected one partially covered aggregate: rows=%#v status=%s coverage=%#v", rows, plan.DataStatus, plan.Coverage)
	}
}

func TestPostgreSQLHistoryReportsCompleteSampledCoverage(t *testing.T) {
	ctx := context.Background()
	target := HistoryTarget{Name: "complete_coverage_target", Engine: EnginePostgreSQL}
	if err := recordHistoryTargetStatus(ctx, target, nil); err != nil {
		t.Fatalf("record sampler status: %v", err)
	}
	sampleInterval := defaultHistorySampleInterval
	if seconds := optionalConfigInt(ctx, "database.observation_history.sample_interval_seconds"); seconds > 0 {
		sampleInterval = time.Duration(seconds) * time.Second
	}
	start := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)
	end := start.Add(2 * sampleInterval)
	for offset := time.Duration(0); offset < 2*sampleInterval; offset += sampleInterval {
		_, err := g.DB().Model("database_observation_intervals").Data(g.Map{
			"target":       target.Name,
			"engine":       string(target.Engine),
			"bucket_start": start.Add(offset),
			"bucket_end":   start.Add(offset + sampleInterval),
		}).Insert()
		if err != nil {
			t.Fatalf("insert coverage interval: %v", err)
		}
	}
	_, err := g.DB().Model("database_observation_samples").Data(g.Map{
		"target":                target.Name,
		"engine":                string(target.Engine),
		"kind":                  string(QuerySlowSQL),
		"statement_fingerprint": "card_service:complete",
		"database_name":         "card_service",
		"statement_preview":     "SELECT pg_sleep(?)",
		"bucket_start":          start,
		"bucket_end":            start.Add(sampleInterval),
		"calls_delta":           1,
		"total_exec_ms_delta":   250.0,
		"time_semantics":        "sampled_delta",
	}).Insert()
	if err != nil {
		t.Fatalf("insert sampled delta: %v", err)
	}

	rangeValue := &TimeRange{Start: start, End: end, Source: TimeRangeExplicit, Semantics: "sampled_delta"}
	plan, err := buildPostgreSQLHistoryPlan(QueryRequest{Kind: QuerySlowSQL, Target: target.Name, ResolvedTimeRange: rangeValue}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build history plan: %v", err)
	}
	plan.Target = target.Name
	rows, err := queryLocalHistory(ctx, &plan)
	if err != nil {
		t.Fatalf("query sampled history: %v", err)
	}
	if len(rows) != 1 || plan.DataStatus != "present" {
		t.Fatalf("expected complete sampled coverage: rows=%#v status=%s coverage=%#v", rows, plan.DataStatus, plan.Coverage)
	}
}

func TestExplicitPostgreSQLHistoryDoesNotReconnectToSourceDatabase(t *testing.T) {
	ctx := context.Background()
	target := HistoryTarget{Name: "offline_history_target", Engine: EnginePostgreSQL}
	if err := recordHistoryTargetStatus(ctx, target, nil); err != nil {
		t.Fatalf("record sampler status: %v", err)
	}
	end := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	start := end.Add(-time.Minute)
	_, err := g.DB().Model("database_observation_samples").Data(g.Map{
		"target":                target.Name,
		"engine":                string(target.Engine),
		"kind":                  string(QuerySlowSQL),
		"statement_fingerprint": "card_service:offline",
		"database_name":         "card_service",
		"statement_preview":     "SELECT pg_sleep(?)",
		"bucket_start":          start,
		"bucket_end":            end,
		"calls_delta":           1,
		"total_exec_ms_delta":   250.0,
		"time_semantics":        "sampled_delta",
	}).Insert()
	if err != nil {
		t.Fatalf("insert sampled delta: %v", err)
	}

	opened := 0
	observer := &Observer{
		Budget:  DefaultResultBudget(),
		Adapter: &postgresqlAdapter{},
		OpenExecutor: func(context.Context, string) (QueryExecutor, error) {
			opened++
			return nil, errors.New("database is offline")
		},
	}
	envelope, err := observer.Query(ctx, QueryRequest{
		Kind:      QuerySlowSQL,
		Target:    target.Name,
		StartTime: start.Format(time.RFC3339),
		EndTime:   end.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("query local history: %v", err)
	}
	if opened != 0 {
		t.Fatalf("explicit local history unexpectedly opened PostgreSQL %d times", opened)
	}
	if envelope.Status != "success" || envelope.Evidence == nil || len(envelope.Evidence.Observations) != 1 {
		t.Fatalf("expected usable offline history evidence, got %#v", envelope)
	}
}

func TestHistoryLeasePreventsConcurrentSampler(t *testing.T) {
	ctx := context.Background()
	config := ObservationHistoryConfig{Enabled: true, SampleInterval: time.Minute, Targets: []HistoryTarget{{Name: "lease_target", Engine: EnginePostgreSQL}}}
	first := &HistorySampler{config: config, owner: "first"}
	second := &HistorySampler{config: config, owner: "second"}
	if acquired, err := first.acquireLease(ctx, "lease_target"); err != nil || !acquired {
		t.Fatalf("first sampler did not acquire lease: acquired=%t err=%v", acquired, err)
	}
	if acquired, err := second.acquireLease(ctx, "lease_target"); err != nil || acquired {
		t.Fatalf("second sampler should not acquire active lease: acquired=%t err=%v", acquired, err)
	}
	if _, err := g.DB().Model("database_observation_leases").Where("target", "lease_target").Data(g.Map{"lease_until": time.Now().UTC().Add(-time.Minute)}).Update(); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if acquired, err := second.acquireLease(ctx, "lease_target"); err != nil || !acquired {
		t.Fatalf("second sampler did not recover expired lease: acquired=%t err=%v", acquired, err)
	}
}

func TestSafeHistoryErrorDoesNotExposeSQLOrConnectionDetails(t *testing.T) {
	err := safeHistoryError("persist PostgreSQL history sample", errors.New("INSERT INTO secret VALUES ('password=top-secret')"))
	message := err.Error()
	if strings.Contains(message, "INSERT") || strings.Contains(message, "top-secret") {
		t.Fatalf("safe history error leaked sensitive details: %s", message)
	}
	if !strings.Contains(message, "sample_failed") {
		t.Fatalf("safe history error omitted error code: %s", message)
	}
}

func TestPostgreSQLHistoryUsesOldSamplesAsPartialAfterTransientFailure(t *testing.T) {
	ctx := context.Background()
	target := HistoryTarget{Name: "transient_failure_target", Engine: EnginePostgreSQL}
	if err := recordHistoryTargetStatus(ctx, target, nil); err != nil {
		t.Fatalf("record successful sample: %v", err)
	}
	if err := recordHistoryTargetStatus(ctx, target, context.DeadlineExceeded); err != nil {
		t.Fatalf("record failed sample: %v", err)
	}

	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-time.Minute)
	_, err := g.DB().Model("database_observation_samples").Data(g.Map{
		"target":                target.Name,
		"engine":                string(target.Engine),
		"kind":                  string(QuerySlowSQL),
		"statement_fingerprint": "card_service:transient",
		"database_name":         "card_service",
		"statement_preview":     "SELECT pg_sleep(?)",
		"bucket_start":          start,
		"bucket_end":            end,
		"calls_delta":           1,
		"total_exec_ms_delta":   250.0,
		"rows_delta":            1,
		"time_semantics":        "sampled_delta",
	}).Insert()
	if err != nil {
		t.Fatalf("insert sampled delta: %v", err)
	}

	rangeValue := &TimeRange{Start: start, End: end, Source: TimeRangeExplicit, Semantics: "sampled_delta"}
	plan, err := buildPostgreSQLHistoryPlan(QueryRequest{Kind: QuerySlowSQL, Target: target.Name, ResolvedTimeRange: rangeValue}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build history plan: %v", err)
	}
	plan.Target = target.Name
	rows, err := queryLocalHistory(ctx, &plan)
	if err != nil {
		t.Fatalf("query old sampled history: %v", err)
	}
	if len(rows) != 1 || plan.DataStatus != "partial" || plan.Coverage["sampler_status"] != "unavailable" {
		t.Fatalf("expected partial evidence after transient failure: rows=%#v status=%s coverage=%#v", rows, plan.DataStatus, plan.Coverage)
	}
}

func TestPostgreSQLHistoryReportsCapabilityGapBeforeFirstSuccess(t *testing.T) {
	ctx := context.Background()
	target := "never_sampled_target"
	end := time.Now().UTC()
	rangeValue := &TimeRange{Start: end.Add(-time.Minute), End: end, Source: TimeRangeExplicit, Semantics: "sampled_delta"}
	plan, err := buildPostgreSQLHistoryPlan(QueryRequest{Kind: QuerySlowSQL, Target: target, ResolvedTimeRange: rangeValue}, DefaultResultBudget())
	if err != nil {
		t.Fatalf("build history plan: %v", err)
	}
	plan.Target = target
	_, err = queryLocalHistory(ctx, &plan)
	if err == nil || !strings.Contains(err.Error(), "sampler has no successful sample") {
		t.Fatalf("expected source capability gap, got %v", err)
	}
}

func TestHistoryCleanupRemovesOnlyExpiredSamples(t *testing.T) {
	ctx := context.Background()
	target := "cleanup_target"
	now := time.Now().UTC()
	insertSample := func(fingerprint string, bucketEnd time.Time) {
		t.Helper()
		_, err := g.DB().Model("database_observation_samples").Data(g.Map{
			"target":                target,
			"engine":                string(EnginePostgreSQL),
			"kind":                  string(QuerySlowSQL),
			"statement_fingerprint": fingerprint,
			"bucket_start":          bucketEnd.Add(-time.Minute),
			"bucket_end":            bucketEnd,
			"calls_delta":           1,
			"time_semantics":        "sampled_delta",
		}).Insert()
		if err != nil {
			t.Fatalf("insert sample %s: %v", fingerprint, err)
		}
	}
	insertSample("expired", now.Add(-49*time.Hour))
	insertSample("retained", now.Add(-47*time.Hour))
	for name, bucketEnd := range map[string]time.Time{
		"expired_interval":  now.Add(-49 * time.Hour),
		"retained_interval": now.Add(-47 * time.Hour),
	} {
		_, err := g.DB().Model("database_observation_intervals").Data(g.Map{
			"target":       target + "_" + name,
			"engine":       string(EnginePostgreSQL),
			"bucket_start": bucketEnd.Add(-time.Minute),
			"bucket_end":   bucketEnd,
		}).Insert()
		if err != nil {
			t.Fatalf("insert sampling interval %s: %v", name, err)
		}
	}

	sampler := &HistorySampler{config: ObservationHistoryConfig{Retention: 48 * time.Hour}}
	if err := sampler.cleanup(ctx); err != nil {
		t.Fatalf("cleanup history: %v", err)
	}
	var samples []struct {
		Fingerprint string `json:"statement_fingerprint"`
	}
	if err := g.DB().Model("database_observation_samples").Fields("statement_fingerprint").Where("target", target).Order("statement_fingerprint").Scan(&samples); err != nil {
		t.Fatalf("read retained samples: %v", err)
	}
	if len(samples) != 1 || samples[0].Fingerprint != "retained" {
		t.Fatalf("cleanup retained unexpected samples: %#v", samples)
	}
	if count, _ := g.DB().Model("database_observation_intervals").Where("target", target+"_expired_interval").Count(); count != 0 {
		t.Fatalf("expired sampling interval was not removed")
	}
	if count, _ := g.DB().Model("database_observation_intervals").Where("target", target+"_retained_interval").Count(); count != 1 {
		t.Fatalf("recent sampling interval was removed")
	}
}
