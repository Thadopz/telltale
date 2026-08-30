package chat_pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestEvidenceSelectorEnforcesGlobalCountAndByteBudget(t *testing.T) {
	items := make([]evidence.EvidenceItem, 0, 30)
	for index := 0; index < 30; index++ {
		items = append(items, evidence.EvidenceItem{
			EvidenceID:      "ev_" + string(rune('a'+index)),
			ToolName:        "query_metrics",
			SourceKind:      evidence.SourceRuntimeMetric,
			RuntimeVerified: true,
			ObservedAt:      time.Now().UTC().Add(time.Duration(index) * time.Second),
			Observations: []evidence.Observation{{
				Kind:    evidence.ObservationMetricValue,
				Summary: "bounded metric",
			}},
		})
	}
	selected, _ := selectModelEvidence(items, nil, nil, nil)
	if len(selected) != maxSelectedEvidence {
		t.Fatalf("expected %d selected evidence items, got %d", maxSelectedEvidence, len(selected))
	}
	if size := encodedSize(selected); size > maxModelEvidenceBytes {
		t.Fatalf("selected evidence exceeded byte budget: %d", size)
	}
}

func TestEvidenceSelectorLimitsEachHypothesisAndDropsExpiredOrInvalidScope(t *testing.T) {
	state := &DiagnosticState{Hypotheses: []Hypothesis{{ID: "runtime_signal"}}}
	items := make([]evidence.EvidenceItem, 0, 8)
	for index := 0; index < 6; index++ {
		items = append(items, evidence.EvidenceItem{
			EvidenceID:      "ev_metric_" + string(rune('a'+index)),
			RuntimeVerified: true,
			Observations:    []evidence.Observation{{Kind: evidence.ObservationMetricAnomaly}},
		})
	}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	items = append(items,
		evidence.EvidenceItem{EvidenceID: "ev_expired", ExpiresAt: &expiredAt, Observations: []evidence.Observation{{Kind: evidence.ObservationMetricAnomaly}}},
		evidence.EvidenceItem{EvidenceID: "ev_bad_range", Observations: []evidence.Observation{{Kind: evidence.ObservationMetricAnomaly, Scope: evidence.EvidenceScope{StartTime: "2026-08-11T12:00:00Z", EndTime: "2026-08-11T11:00:00Z"}}}},
	)
	selected, _ := selectModelEvidence(items, nil, state, nil)
	if len(selected) != maxEvidencePerHypothesis {
		t.Fatalf("expected %d evidence items for one hypothesis, got %d", maxEvidencePerHypothesis, len(selected))
	}
	for _, item := range selected {
		if item.EvidenceID == "ev_expired" || item.EvidenceID == "ev_bad_range" {
			t.Fatalf("selector retained unusable evidence: %s", item.EvidenceID)
		}
	}
}

func TestEvidenceSelectorCompactsOversizedDetails(t *testing.T) {
	large := strings.Repeat("x", 48*1024)
	items := []evidence.EvidenceItem{
		{EvidenceID: "ev_1", RuntimeVerified: true, Observations: []evidence.Observation{{Kind: evidence.ObservationMetricValue, Fields: map[string]any{"payload": large}, Summary: large}}},
		{EvidenceID: "ev_2", RuntimeVerified: true, Observations: []evidence.Observation{{Kind: evidence.ObservationSlowSQL, Fields: map[string]any{"payload": large}, Summary: large}}},
	}
	selected, _ := selectModelEvidence(items, nil, nil, nil)
	if len(selected) != 2 {
		t.Fatalf("expected compact forms to retain both summaries, got %d", len(selected))
	}
	if size := encodedSize(selected); size > maxModelEvidenceBytes {
		t.Fatalf("compacted evidence exceeded byte budget: %d", size)
	}
	if len(selected[1].Observations[0].Fields) != 0 || !strings.Contains(selected[1].Summary, "omitted") {
		t.Fatalf("second oversized item was not compacted: %#v", selected[1])
	}
}

func TestEvidenceSelectorRejectsSlowSQLOutsidePinnedIncidentScope(t *testing.T) {
	state := &DiagnosticState{
		ExpectedStartTime:      "2026-08-11T10:00:00Z",
		ExpectedEndTime:        "2026-08-11T10:30:00Z",
		ExpectedDatabaseTarget: "card_service_primary",
		Hypotheses:             []Hypothesis{{ID: "dependency_or_log"}},
	}
	newItem := func(id, start, end, target string) evidence.EvidenceItem {
		return evidence.EvidenceItem{
			EvidenceID: id,
			Observations: []evidence.Observation{{Kind: evidence.ObservationSlowSQL, Scope: evidence.EvidenceScope{
				StartTime: start, EndTime: end, Fields: map[string]string{"target": target},
			}}},
		}
	}
	items := []evidence.EvidenceItem{
		newItem("ev_exact", state.ExpectedStartTime, state.ExpectedEndTime, state.ExpectedDatabaseTarget),
		newItem("ev_outside", "2026-08-11T09:00:00Z", "2026-08-11T09:30:00Z", state.ExpectedDatabaseTarget),
		newItem("ev_wrong_target", state.ExpectedStartTime, state.ExpectedEndTime, "reporting"),
	}
	selected, _ := selectModelEvidence(items, nil, state, nil)
	if len(selected) != 1 || selected[0].EvidenceID != "ev_exact" {
		t.Fatalf("selector retained out-of-scope database evidence: %#v", selected)
	}
}
