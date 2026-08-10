package chat_pipeline

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestBuildDiagnosticStateAddsResourceHypothesisForPoolQuestion(t *testing.T) {
	state := BuildDiagnosticState("Why did the database connection pool run out while traffic stayed normal?")

	if state.Round != 1 || state.MaxRounds != diagnosticMaxRounds {
		t.Fatalf("unexpected diagnostic bounds: %#v", state)
	}
	if len(state.Hypotheses) != 4 {
		t.Fatalf("expected four hypotheses for a pool question, got %d", len(state.Hypotheses))
	}
	if state.Hypotheses[3].ID != "traffic_or_resource_pressure" {
		t.Fatalf("expected resource hypothesis last, got %#v", state.Hypotheses[3])
	}
}

func TestUpdateDiagnosticStateDoesNotTreatFailedQueryAsRefutation(t *testing.T) {
	state := BuildDiagnosticState("Why are payment callbacks failing?")
	failed := trace.ToolEvidence{
		ToolName: "query_payment_metrics",
		Envelope: evidence.NewError(nil, "query timeout"),
	}

	UpdateDiagnosticState(&state, []trace.ToolEvidence{failed})

	for _, hypothesis := range state.Hypotheses {
		if hypothesis.Status != HypothesisOpen {
			t.Fatalf("failed query changed hypothesis %s to %s", hypothesis.ID, hypothesis.Status)
		}
	}
}

func TestRunDiagnosticLoopPerformsAtMostOneFollowUp(t *testing.T) {
	recorder := trace.NewRecorder()
	runner := &diagnosticTestRunner{recorder: recorder}
	input := &UserMessage{Query: "Why is the payment callback failing?"}

	answer, err := RunDiagnosticLoop(context.Background(), runner, input, recorder)
	if err != nil {
		t.Fatalf("run diagnostic loop: %v", err)
	}
	if answer == nil || answer.Content == "" {
		t.Fatal("expected an answer")
	}
	if runner.calls != diagnosticMaxRounds {
		t.Fatalf("expected one initial call and one follow-up, got %d calls", runner.calls)
	}
	if !strings.Contains(runner.lastQuery, "Diagnostic control") {
		t.Fatalf("expected diagnostic control in follow-up query, got %q", runner.lastQuery)
	}
}

type diagnosticTestRunner struct {
	recorder  *trace.Recorder
	calls     int
	lastQuery string
}

func (r *diagnosticTestRunner) Invoke(_ context.Context, input *UserMessage, _ ...compose.Option) (*schema.Message, error) {
	r.calls++
	r.lastQuery = input.Query
	r.recorder.RecordToolEvidence(trace.ToolEvidence{
		ToolName: "query_payment_metrics",
		Envelope: evidence.NewDataWithObservations(
			map[string]any{"failure_rate": 0.2},
			"payment metric observed",
			[]evidence.Observation{{Kind: evidence.ObservationMetricAnomaly}},
		),
	})
	return schema.AssistantMessage("diagnostic answer", nil), nil
}

func (r *diagnosticTestRunner) Stream(_ context.Context, _ *UserMessage, _ ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("diagnostic answer", nil)}), nil
}

func (r *diagnosticTestRunner) Collect(_ context.Context, input *schema.StreamReader[*UserMessage], _ ...compose.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("collect is not used in diagnostic loop test")
}

func (r *diagnosticTestRunner) Transform(_ context.Context, input *schema.StreamReader[*UserMessage], _ ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("transform is not used in diagnostic loop test")
}
