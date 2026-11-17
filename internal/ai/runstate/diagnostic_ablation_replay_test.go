package runstate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNoFeedbackReplaysRealObservationsWithoutPredictionLabels(t *testing.T) {
	path := os.Getenv("TELLTALE_DIAGNOSTIC_SNAPSHOT")
	if path == "" {
		t.Skip("set TELLTALE_DIAGNOSTIC_SNAPSHOT to a real recorded diagnostic snapshot")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state Diagnostic
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Facts) == 0 || len(state.Candidates) < 2 {
		t.Fatal("recorded replay requires actual observations and competing plans")
	}
	state.ablation = "no_feedback"
	state.checked = make(map[string]bool)
	state.Transitions = nil
	for i := range state.Candidates {
		candidate := &state.Candidates[i]
		for _, check := range candidate.Checks {
			if check.Status != "pending" {
				state.checked[queryKey(check.Tool, check.Arguments)] = true
			}
		}
		candidate.Status = "open"
	}
	state.update()
	for _, candidate := range state.Candidates {
		if candidate.Status != "unassessed" || len(candidate.OpposingEvidenceIDs) != 0 {
			t.Fatal("prediction feedback escaped the coverage-only ablation")
		}
		for _, check := range candidate.Checks {
			if check.Status != "completed" && check.Status != "pending" {
				t.Fatal("check status contains prediction feedback")
			}
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"supported"`) || strings.Contains(string(encoded), `"refuted"`) {
		t.Fatal("snapshot contains automatic support/refutation labels")
	}
}
