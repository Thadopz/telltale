package mem

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/gogf/gf/v2/frame/g"
)

func TestRealDiagnosticStateSurvivesPersistenceAndBounding(t *testing.T) {
	path := os.Getenv("TELLTALE_DIAGNOSTIC_AUDIT")
	if path == "" {
		t.Skip("set TELLTALE_DIAGNOSTIC_AUDIT to a real diagnostic replay snapshot")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot runstate.Diagnostic
	if err := json.Unmarshal(content, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Candidates) < 2 || len(snapshot.Facts) < 2 {
		t.Fatal("real snapshot lacks competing hypotheses or measured facts")
	}
	user, conversation := "diagnostic-retention", "real-diagnostic-retention"
	scope := normalizeMemoryScope(user, conversation)
	t.Cleanup(func() {
		_, _ = g.DB().Model("session_states").Where("conversation_id", scope.StorageID).Delete()
	})
	if err := UpdateSessionState(t.Context(), user, conversation, "diagnose the historical case", "candidate submitted with unconfirmed causality", string(content)); err != nil {
		t.Fatal(err)
	}
	stored, err := loadSessionStateRow(t.Context(), scope.StorageID)
	if err != nil {
		t.Fatal(err)
	}
	text := formatSessionState(stored)
	bounded := BoundContextSummary(text, 6000, false)
	for _, candidate := range snapshot.Candidates {
		if !strings.Contains(bounded, candidate.ID) || !strings.Contains(bounded, candidate.Status) {
			t.Fatal("competing hypothesis state lost on persistence or compaction")
		}
	}
	for _, fact := range snapshot.Facts[:2] {
		encoded, _ := json.Marshal(fact)
		if !strings.Contains(bounded, string(encoded)) {
			t.Fatal("exact measured comparison or evidence reference lost")
		}
	}
	for _, line := range strings.Split(BoundContextSummary(text, 1500, false), "\n") {
		for _, prefix := range []string{"hypothesis: ", "observation: ", "pending_check: "} {
			if strings.HasPrefix(line, prefix) && !json.Valid([]byte(strings.TrimPrefix(line, prefix))) {
				t.Fatal("context budget cut a structured record in half")
			}
		}
	}
}
