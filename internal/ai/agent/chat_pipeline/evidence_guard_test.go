package chat_pipeline

import (
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/ai/evidence"
	"strings"
	"testing"
)

func TestComposeCoverageAnswerUsesGenericRulesWithoutPaymentSignal(t *testing.T) {
	got := composeCoverageAnswer("service has error logs", []trace.AgentStep{
		failedToolStep("kubectl_logs", "fq_1"),
	})
	if !strings.Contains(got, evidence.ClaimLogError) {
		t.Fatalf("expected generic log claim, got %q", got)
	}
	if strings.Contains(got, evidence.ClaimPaymentLogError) {
		t.Fatalf("did not expect payment claim, got %q", got)
	}
}

func TestComposeCoverageAnswerEnablesPaymentRulesForPaymentSignal(t *testing.T) {
	got := composeCoverageAnswer("payment failure has error logs", []trace.AgentStep{
		failedToolStep("search_payment_logs", "fq_1"),
	})
	if !strings.Contains(got, evidence.ClaimPaymentLogError) {
		t.Fatalf("expected payment log claim, got %q", got)
	}
}

func TestComposeCoverageAnswerKeepsCallbackPaymentRule(t *testing.T) {
	got := composeCoverageAnswer("payment callback root cause confirmed for failure timeout log", []trace.AgentStep{
		successToolStep("query_payment_anomaly", "ev_1", evidence.ObservationMetricAnomaly, evidence.SubjectRef{Type: "payment", Service: "payment"}),
		successToolStep("search_payment_logs", "ev_2", evidence.ObservationLogError, evidence.SubjectRef{Type: "service", Service: "callback"}),
	})
	if !strings.Contains(got, evidence.ClaimCallbackTimeoutRootCause) {
		t.Fatalf("expected callback root cause claim, got %q", got)
	}
	if !strings.Contains(got, "mechanism_or_runbook") {
		t.Fatalf("expected callback rule to require mechanism evidence, got %q", got)
	}
}

func failedToolStep(name string, failedQueryID string) trace.AgentStep {
	return trace.AgentStep{
		Phase: "error",
		Name:  name,
		Output: evidence.Marshal(evidence.Envelope{
			Success:       false,
			Status:        evidence.StatusTimeout,
			FailedQueryID: failedQueryID,
			DataStatus:    evidence.DataUnknown,
			Message:       "tool query timed out",
			Summary:       "tool query timed out",
		}),
	}
}

func successToolStep(name string, evidenceID string, observationKind string, subject evidence.SubjectRef) trace.AgentStep {
	return trace.AgentStep{
		Phase: "end",
		Name:  name,
		Output: evidence.Marshal(evidence.Envelope{
			Success:          true,
			Status:           evidence.StatusSuccess,
			EvidenceID:       evidenceID,
			CanUseAsEvidence: true,
			DataStatus:       evidence.DataPresent,
			Data:             map[string]string{"result": "present"},
			Subject:          subject,
			Observations: []evidence.Observation{{
				Kind:    observationKind,
				Subject: subject,
				Summary: observationKind,
			}},
			Message: "tool query completed",
			Summary: "tool query completed",
		}),
	}
}
