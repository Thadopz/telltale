package watcher

import (
	"context"
	"errors"
	"testing"
	"time"
)

type watcherTestCollector struct {
	snapshot Snapshot
	err      error
}

func (c watcherTestCollector) Collect(context.Context, Rule, time.Time) (Snapshot, error) {
	return c.snapshot, c.err
}

type watcherTestStore struct {
	state     DetectorState
	incidents []Incident
}

func (s *watcherTestStore) LoadState(context.Context, string) (DetectorState, error) {
	return s.state, nil
}

func (s *watcherTestStore) SaveState(_ context.Context, state DetectorState) error {
	s.state = state
	return nil
}

func (s *watcherTestStore) SaveIncident(_ context.Context, incident Incident) error {
	s.incidents = append(s.incidents, incident)
	return nil
}

func TestRunnerOpensIncidentOnStateTransition(t *testing.T) {
	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	rule := Rule{
		ID:              "critical-alerts",
		Domain:          "observability",
		Collector:       CollectorPrometheusAlerts,
		Operator:        OperatorCountAbove,
		Threshold:       0,
		Severity:        "critical",
		FiringWindows:   1,
		RecoveryWindows: 1,
	}
	store := &watcherTestStore{state: DetectorState{RuleID: rule.ID, State: StateNormal}}
	collector := watcherTestCollector{snapshot: Snapshot{
		RuleID:      rule.ID,
		CollectedAt: now,
		Value:       2,
		EvidenceID:  "ev_alerts",
		RawOutput:   `{"status":"success"}`,
	}}
	runner := NewRunner(Config{
		Enabled:     true,
		TenantID:    "tenant-a",
		Environment: "prod",
		Rules:       []Rule{rule},
	}, collector, store)
	runner.now = func() time.Time { return now }

	results, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run watcher: %v", err)
	}
	if len(results) != 1 || results[0].Transition != TransitionOpened {
		t.Fatalf("unexpected results: %#v", results)
	}
	if len(store.incidents) != 1 || store.incidents[0].Status != StateFiring {
		t.Fatalf("expected one firing incident, got %#v", store.incidents)
	}
	if store.incidents[0].EvidenceID != "ev_alerts" {
		t.Fatalf("expected evidence to be retained, got %#v", store.incidents[0])
	}
}

func TestRunnerCollectionFailureKeepsExistingIncidentFiring(t *testing.T) {
	rule := Rule{ID: "critical-alerts", Domain: "observability", Collector: CollectorPrometheusAlerts}
	store := &watcherTestStore{state: DetectorState{RuleID: rule.ID, State: StateFiring}}
	runner := NewRunner(Config{Rules: []Rule{rule}}, watcherTestCollector{err: errors.New("prometheus unavailable")}, store)

	results, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if len(results) != 1 || results[0].State.State != StateFiring {
		t.Fatalf("expected firing state to remain, got %#v", results)
	}
	if len(store.incidents) != 0 {
		t.Fatalf("expected no incident update from failed collection, got %#v", store.incidents)
	}
}
