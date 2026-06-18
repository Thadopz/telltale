package chat_pipeline

import (
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/ai/evidence"
	"context"
	"strings"
	"testing"
)

func TestConvergeAnswerEmptyAnswerUsesFinalizerWithToolEvidence(t *testing.T) {
	item := testSREGymToolEvidence()
	got, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "diagnose incident",
		ToolEvidence: []trace.ToolEvidence{item},
		Finalize: func(ctx context.Context, question string, tools []trace.ToolEvidence) (string, error) {
			return "faulty component(s): product-catalog\nroot cause: ImagePullBackOff on ev_1781586631442_e9100119\nevidence from tools: ev_1781586631442_e9100119", nil
		},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty answer")
	}
	if !strings.Contains(got, "ev_1781586631442_e9100119") {
		t.Fatalf("expected evidence id in answer, got %q", got)
	}
}

func TestConvergeAnswerUsesStructuredEvidenceWhenTraceOutputIsTruncated(t *testing.T) {
	item := testSREGymToolEvidence()
	got, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "diagnose incident",
		Steps: []trace.AgentStep{{
			Phase:  "end",
			Name:   "exec_kubectl_cmd_safely",
			Output: `{"success":true,"status":"success","evidence_id":"ev_1781586631442...`,
		}},
		ToolEvidence: []trace.ToolEvidence{item},
		Finalize: func(ctx context.Context, question string, tools []trace.ToolEvidence) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if !strings.Contains(got, "ev_1781586631442_e9100119") {
		t.Fatalf("expected full structured evidence id despite truncated trace, got %q", got)
	}
	if !strings.Contains(got, "Verified evidence") {
		t.Fatalf("expected generic structured evidence fallback, got %q", got)
	}
}

func TestConvergeAnswerFinalizerEmptyFallsBackToDeterministicAnswer(t *testing.T) {
	got, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "diagnose incident",
		ToolEvidence: []trace.ToolEvidence{testSREGymToolEvidence()},
		Finalize: func(ctx context.Context, question string, tools []trace.ToolEvidence) (string, error) {
			return "   ", nil
		},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	for _, want := range []string{"Verified evidence", "ev_1781586631442_e9100119", "exec_kubectl_cmd_safely"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in fallback answer, got %q", want, got)
		}
	}
	if strings.Contains(got, "faulty component(s)") {
		t.Fatalf("expected generic fallback without SREGym Kubernetes headings, got %q", got)
	}
}

func TestDeterministicEvidenceAnswerDoesNotUseSREGymKubernetesFormat(t *testing.T) {
	got := deterministicEvidenceAnswer("diagnose SREGym Kubernetes incident", []trace.ToolEvidence{testSREGymToolEvidence()})
	for _, want := range []string{"Verified evidence", "ev_1781586631442_e9100119", "exec_kubectl_cmd_safely"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in generic evidence answer, got %q", want, got)
		}
	}
	if strings.Contains(got, "faulty component(s)") {
		t.Fatalf("expected SREGym Kubernetes fallback to be disabled, got %q", got)
	}
}

func TestConvergeAnswerNonEmptyAnswerRegression(t *testing.T) {
	got, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "hello",
		Answer:   "plain final answer",
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if got != "plain final answer" {
		t.Fatalf("expected answer unchanged, got %q", got)
	}
}

func TestConvergeAnswerNoEvidenceDoesNotInventRootCause(t *testing.T) {
	got, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "diagnose incident",
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty no-evidence answer")
	}
	if strings.Contains(strings.ToLower(got), "root cause") {
		t.Fatalf("no-evidence answer should not invent a root cause, got %q", got)
	}
}

func testSREGymToolEvidence() trace.ToolEvidence {
	env := evidence.Envelope{
		Success:          true,
		Status:           evidence.StatusSuccess,
		EvidenceID:       "ev_1781586631442_e9100119",
		CanUseAsEvidence: true,
		DataStatus:       evidence.DataPresent,
		Data: `Name: product-catalog-6d8d44485-r2bvc
Containers:
  product-catalog:
    Image: app-image:latest
    State:
      Reason: ImagePullBackOff`,
		Observations: []evidence.Observation{{
			Kind:    evidence.ObservationMetricValue,
			Summary: "kubectl describe pod returned output",
		}},
		Message: "SREGym tool exec_kubectl_cmd_safely returned output",
		Summary: "SREGym tool exec_kubectl_cmd_safely returned output",
	}
	return trace.ToolEvidence{
		ToolName:   "exec_kubectl_cmd_safely",
		InputJSON:  `{"cmd":"kubectl describe pod product-catalog-6d8d44485-r2bvc -n astronomy-shop"}`,
		OutputJSON: evidence.Marshal(env),
		Envelope:   env,
		EvidenceID: env.EvidenceID,
		Status:     env.Status,
		Summary:    env.Summary,
	}
}
