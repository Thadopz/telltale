package contextbudget

import (
	"context"
	"sync"
	"testing"
)

func TestCalculateUsesWorkingCapForLargeModel(t *testing.T) {
	budget, err := Calculate(ModelContextProfile{
		ModelName:             "large-model",
		ContextWindowTokens:   1_048_576,
		MaxOutputTokens:       8192,
		WorkingPromptCap:      131072,
		SafetyMarginRatio:     0.10,
		MinSafetyMarginTokens: 2048,
	}, 10000, 20000, 1)
	if err != nil {
		t.Fatalf("calculate budget: %v", err)
	}
	if budget.InputLimitTokens != 131072 {
		t.Fatalf("expected working cap to limit input, got %d", budget.InputLimitTokens)
	}
	if budget.ContentBudgetTokens != 107964 {
		t.Fatalf("unexpected content budget, got %d", budget.ContentBudgetTokens)
	}
	if budget.OutputReserveTokens != 8192 || budget.PromptOverheadTokens != 10000 {
		t.Fatalf("unexpected budget accounting: %#v", budget)
	}
}

func TestMergeProfilesUsesSafeFailoverIntersection(t *testing.T) {
	merged := MergeProfiles(
		ModelContextProfile{
			ConfigKey:             "quick_chat_model",
			ModelName:             "large-model",
			ContextWindowTokens:   1_048_576,
			MaxOutputTokens:       4096,
			WorkingPromptCap:      131072,
			SafetyMarginRatio:     0.10,
			MinSafetyMarginTokens: 2048,
		},
		&ModelContextProfile{
			ConfigKey:             "failover_chat_model",
			ModelName:             "small-model",
			ContextWindowTokens:   32768,
			MaxOutputTokens:       8192,
			WorkingPromptCap:      65536,
			SafetyMarginRatio:     0.15,
			MinSafetyMarginTokens: 4096,
		},
	)
	if merged.ContextWindowTokens != 32768 || merged.WorkingPromptCap != 65536 {
		t.Fatalf("expected failover window intersection, got %#v", merged)
	}
	if merged.MaxOutputTokens != 8192 || merged.SafetyMarginRatio != 0.15 || merged.MinSafetyMarginTokens != 4096 {
		t.Fatalf("expected conservative output and margin settings, got %#v", merged)
	}
}

func TestCalibratorUpdatesAndPersistsOnlyAggregateUsage(t *testing.T) {
	store := &fakeCalibrationStore{records: make(map[string]CalibrationRecord)}
	calibrator := NewCalibrator(store)
	ctx := context.Background()

	calibrator.Observe(ctx, "quick:model-a", 100, 200)
	if got := calibrator.Factor(ctx, "quick:model-a"); got <= 1 || got > 1.5 {
		t.Fatalf("expected calibration factor above one, got %f", got)
	}
	if store.records["quick:model-a"].LastPrompt != 200 || store.records["quick:model-a"].SampleCount != 1 {
		t.Fatalf("expected aggregate calibration record, got %#v", store.records["quick:model-a"])
	}

	reloaded := NewCalibrator(store)
	if got := reloaded.Factor(ctx, "quick:model-a"); got <= 1 {
		t.Fatalf("expected persisted factor, got %f", got)
	}

	calibrator.Observe(ctx, "quick:model-a", 100, 50)
	if got := calibrator.Factor(ctx, "quick:model-a"); got < 1 {
		t.Fatalf("calibration must never become optimistic, got %f", got)
	}
}

type fakeCalibrationStore struct {
	mu      sync.Mutex
	records map[string]CalibrationRecord
}

func (s *fakeCalibrationStore) Load(_ context.Context, key string) (CalibrationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[key], nil
}

func (s *fakeCalibrationStore) Save(_ context.Context, record CalibrationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ModelKey] = record
	return nil
}
