package watcher

import (
	"errors"
	"testing"
	"time"
)

func TestApplySnapshotOpensAndResolvesAfterConfiguredWindows(t *testing.T) {
	rule := Rule{
		ID:              "payment-success-rate",
		Operator:        OperatorLessThan,
		Threshold:       0.98,
		FiringWindows:   3,
		RecoveryWindows: 2,
	}
	state := DetectorState{RuleID: rule.ID, State: StateNormal}
	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)

	for index := 0; index < 2; index++ {
		var transition string
		state, transition = ApplySnapshot(state, rule, Snapshot{Value: 0.90, CollectedAt: now.Add(time.Duration(index) * time.Minute)})
		if transition != TransitionNone || state.State != StateNormal {
			t.Fatalf("expected normal before third failure, got state=%s transition=%s", state.State, transition)
		}
	}

	state, transition := ApplySnapshot(state, rule, Snapshot{Value: 0.90, CollectedAt: now.Add(2 * time.Minute)})
	if transition != TransitionOpened || state.State != StateFiring {
		t.Fatalf("expected incident to open, got state=%s transition=%s", state.State, transition)
	}

	state, transition = ApplySnapshot(state, rule, Snapshot{Value: 0.99, CollectedAt: now.Add(3 * time.Minute)})
	if transition != TransitionNone || state.State != StateFiring {
		t.Fatalf("expected first recovery window to remain firing, got state=%s transition=%s", state.State, transition)
	}
	state, transition = ApplySnapshot(state, rule, Snapshot{Value: 0.99, CollectedAt: now.Add(4 * time.Minute)})
	if transition != TransitionResolved || state.State != StateNormal {
		t.Fatalf("expected incident to resolve, got state=%s transition=%s", state.State, transition)
	}
}

func TestMinimumSamplesSuppressesSmallWindows(t *testing.T) {
	rule := Rule{
		Operator:       OperatorLessThan,
		Threshold:      0.98,
		MinimumSamples: 100,
	}
	if IsAbnormal(rule, Snapshot{Value: 0.20, SampleCount: 5}) {
		t.Fatal("expected a low-volume window to be suppressed")
	}
	if !IsAbnormal(rule, Snapshot{Value: 0.90, SampleCount: 200}) {
		t.Fatal("expected a sufficiently large abnormal window")
	}
}

func TestRecordCollectionErrorDoesNotResolveFiringIncident(t *testing.T) {
	state := DetectorState{RuleID: "alerts", State: StateFiring, ConsecutiveFailures: 3}
	state = RecordCollectionError(state, "alerts", errors.New("prometheus unavailable"), time.Now())
	if state.State != StateFiring {
		t.Fatalf("expected firing state to remain unchanged, got %s", state.State)
	}
	if state.LastError == "" {
		t.Fatal("expected collection error to be recorded")
	}
}
