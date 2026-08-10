package chat_pipeline

import (
	"context"
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"testing"
	"time"
)

func TestConvergeAnswerMatchesStructuredClaimsAndLocalizesChinese(t *testing.T) {
	item := structuredToolEvidence("runtime_metrics", "ev_metric", evidence.SourceRuntimeMetric, evidence.ObservationMetricAnomaly, evidence.SubjectRef{Service: "payment"})
	extractor := &fixedClaimExtractor{claimSet: evidence.ClaimSet{
		SchemaVersion: evidence.ClaimSchemaVersion,
		Claims: []evidence.CandidateClaim{{
			ClaimID:       "claim-1",
			ClaimType:     evidence.ClaimPaymentMetricAbnormal,
			AssertionKind: evidence.AssertionFact,
			EvidenceIDs:   []string{"ev_metric"},
			Subject:       evidence.SubjectRef{Service: "payment"},
		}},
	}}
	recorder := trace.NewRecorder()
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:       "支付最近是否异常？",
		Answer:         "draft wording should not decide coverage",
		ToolEvidence:   []trace.ToolEvidence{item},
		ClaimExtractor: extractor,
		Recorder:       recorder,
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if !strings.Contains(answer, "已验证事实") || !strings.Contains(answer, "ev_metric") {
		t.Fatalf("expected localized covered answer, got %q", answer)
	}
	if strings.Contains(answer, "draft wording") {
		t.Fatalf("draft answer must not be used as final evidence coverage: %q", answer)
	}
	steps := recorder.Steps()
	if len(steps) != 1 || !steps[0].ClaimExtractorSuccess || steps[0].ClaimCount != 1 {
		t.Fatalf("expected structured claim trace metadata, got %#v", steps)
	}
}

func TestConvergeAnswerDoesNotInferClaimFromKeywordOnlyDraft(t *testing.T) {
	item := structuredToolEvidence("runtime_logs", "ev_log", evidence.SourceRuntimeLog, evidence.ObservationLogError, evidence.SubjectRef{Service: "payment"})
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "payment timeout",
		Answer:       "payment timeout has error logs",
		ToolEvidence: []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{claimSet: evidence.ClaimSet{
			SchemaVersion: evidence.ClaimSchemaVersion,
			Claims:        []evidence.CandidateClaim{},
		}},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if strings.Contains(answer, evidence.ClaimPaymentLogError) || strings.Contains(answer, "Verified facts") {
		t.Fatalf("keyword-only draft must not create coverage, got %q", answer)
	}
	if !strings.Contains(answer, "structured Claim") || !strings.Contains(answer, "No coverage result") {
		t.Fatalf("expected conservative structured-claim gap, got %q", answer)
	}
}

func TestConvergeAnswerRejectsExtractorFailureWithoutKeywordFallback(t *testing.T) {
	item := structuredToolEvidence("runtime_logs", "ev_log", evidence.SourceRuntimeLog, evidence.ObservationLogError, evidence.SubjectRef{Service: "payment"})
	recorder := trace.NewRecorder()
	_, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:       "payment timeout",
		Answer:         "payment timeout",
		ToolEvidence:   []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{err: context.DeadlineExceeded},
		Recorder:       recorder,
	})
	if err == nil {
		t.Fatal("expected claim extraction failure")
	}
	steps := recorder.Steps()
	if len(steps) != 1 || steps[0].ClaimExtractorSuccess {
		t.Fatalf("expected failed claim extraction trace, got %#v", steps)
	}
}

func TestConvergeAnswerRequiresExplicitEvidenceReference(t *testing.T) {
	item := structuredToolEvidence("runtime_logs", "ev_log", evidence.SourceRuntimeLog, evidence.ObservationLogError, evidence.SubjectRef{Service: "payment"})
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "check logs",
		ToolEvidence: []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{claimSet: evidence.ClaimSet{
			SchemaVersion: evidence.ClaimSchemaVersion,
			Claims: []evidence.CandidateClaim{{
				ClaimID:       "claim-1",
				ClaimType:     evidence.ClaimPaymentLogError,
				AssertionKind: evidence.AssertionFact,
				Subject:       evidence.SubjectRef{Service: "payment"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if strings.Contains(answer, "已验证事实") || strings.Contains(answer, "Verified facts") {
		t.Fatalf("claim without evidence reference must not be covered, got %q", answer)
	}
	if !strings.Contains(answer, "evidence_reference") {
		t.Fatalf("expected explicit evidence reference gap, got %q", answer)
	}
}

func TestConvergeAnswerUsesFailedQueryAsUnknownOnly(t *testing.T) {
	failed := trace.ToolEvidence{
		ToolName: "runtime_logs",
		Envelope: evidence.Envelope{
			Success:       false,
			Status:        evidence.StatusTimeout,
			FailedQueryID: "fq_logs",
			Summary:       "log query timed out",
		},
	}
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "check logs",
		ToolEvidence: []trace.ToolEvidence{failed},
		ClaimExtractor: &fixedClaimExtractor{claimSet: evidence.ClaimSet{
			SchemaVersion: evidence.ClaimSchemaVersion,
			Claims: []evidence.CandidateClaim{{
				ClaimID:        "claim-1",
				ClaimType:      evidence.ClaimPaymentLogError,
				AssertionKind:  evidence.AssertionHypothesis,
				FailedQueryIDs: []string{"fq_logs"},
				Subject:        evidence.SubjectRef{Service: "payment"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if !strings.Contains(answer, "Unknown or failed checks") || !strings.Contains(answer, "fq_logs") {
		t.Fatalf("expected unknown failed-query coverage, got %q", answer)
	}
	if strings.Contains(answer, "Verified facts") {
		t.Fatalf("failed query must not become verified evidence, got %q", answer)
	}
}

func TestClaimCoverageDoesNotDependOnAnswerLanguage(t *testing.T) {
	item := structuredToolEvidence("runtime_logs", "ev_log", evidence.SourceRuntimeLog, evidence.ObservationLogError, evidence.SubjectRef{Service: "payment"})
	claimSet := evidence.ClaimSet{
		SchemaVersion: evidence.ClaimSchemaVersion,
		Claims: []evidence.CandidateClaim{{
			ClaimID:       "claim-1",
			ClaimType:     evidence.ClaimPaymentLogError,
			AssertionKind: evidence.AssertionFact,
			EvidenceIDs:   []string{"ev_log"},
			Subject:       evidence.SubjectRef{Service: "payment"},
		}},
	}
	first := &ClaimPipelineResult{}
	second := &ClaimPipelineResult{}
	_, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:       "Why did the payment callback fail?",
		Answer:         "The English draft is untrusted.",
		ToolEvidence:   []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{claimSet: claimSet},
		PipelineResult: first,
	})
	if err != nil {
		t.Fatalf("English convergence failed: %v", err)
	}
	_, err = ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:       "鏀粯鍥炶皟涓轰粈涔堝け璐ワ紵",
		Answer:         "涓枃鑽夌涓嶄綔涓鸿瘉鎹€�",
		ToolEvidence:   []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{claimSet: claimSet},
		PipelineResult: second,
	})
	if err != nil {
		t.Fatalf("Chinese convergence failed: %v", err)
	}
	firstJSON, _ := json.Marshal(first.CoverageReports)
	secondJSON, _ := json.Marshal(second.CoverageReports)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("coverage changed with answer language: English=%s Chinese=%s", firstJSON, secondJSON)
	}
}

func TestConvergeAnswerReturnsEvidenceGapWithoutEvidence(t *testing.T) {
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "hello",
		Answer:   "plain final answer",
	})
	if err != nil {
		t.Fatalf("ConvergeAnswer returned error: %v", err)
	}
	if answer == "plain final answer" || !strings.Contains(answer, "No successful evidence_id") {
		t.Fatalf("expected an evidence gap answer, got %q", answer)
	}
}

func TestConvergeAnswerKeepsNormalChatWithoutEvidence(t *testing.T) {
	answer, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question: "hello",
		Answer:   "hello, how can I help?",
		Mode:     ResponseModeNormal,
	})
	if err != nil {
		t.Fatalf("normal chat should not require evidence: %v", err)
	}
	if answer != "hello, how can I help?" {
		t.Fatalf("unexpected normal chat answer %q", answer)
	}
}

type fixedAnswerLocalizer struct {
	answer string
}

func (localizer fixedAnswerLocalizer) Localize(context.Context, string, []evidence.CoverageReport) (string, error) {
	return localizer.answer, nil
}

func TestCustomLocalizerCannotAddEvidenceOutsideCoverageReport(t *testing.T) {
	item := structuredToolEvidence("runtime_logs", "ev_log", evidence.SourceRuntimeLog, evidence.ObservationLogError, evidence.SubjectRef{Service: "payment"})
	_, err := ConvergeAnswer(context.Background(), AnswerConvergeInput{
		Question:     "check logs",
		ToolEvidence: []trace.ToolEvidence{item},
		ClaimExtractor: &fixedClaimExtractor{claimSet: evidence.ClaimSet{
			SchemaVersion: evidence.ClaimSchemaVersion,
			Claims: []evidence.CandidateClaim{{
				ClaimID:       "claim-1",
				ClaimType:     evidence.ClaimPaymentLogError,
				AssertionKind: evidence.AssertionFact,
				EvidenceIDs:   []string{"ev_log"},
				Subject:       evidence.SubjectRef{Service: "payment"},
			}},
		}},
		Localizer: fixedAnswerLocalizer{answer: "covered ev_log and ev_unreferenced"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown evidence_id ev_unreferenced") {
		t.Fatalf("expected custom localizer evidence validation failure, got %v", err)
	}
}

func structuredToolEvidence(name string, evidenceID string, sourceKind evidence.EvidenceSourceKind, observationKind string, subject evidence.SubjectRef) trace.ToolEvidence {
	env := evidence.Envelope{
		Success:          true,
		Status:           evidence.StatusSuccess,
		EvidenceID:       evidenceID,
		CanUseAsEvidence: true,
		DataStatus:       evidence.DataPresent,
		SourceKind:       sourceKind,
		RuntimeVerified:  sourceKind == evidence.SourceRuntimeMetric || sourceKind == evidence.SourceRuntimeLog || sourceKind == evidence.SourceRuntimeDB,
		ObservedAt:       time.Now().UTC(),
		Subject:          subject,
		Observations: []evidence.Observation{{
			Kind:    observationKind,
			Subject: subject,
		}},
		Summary: "structured runtime evidence",
	}
	return trace.ToolEvidence{
		ToolName:   name,
		Envelope:   env,
		EvidenceID: evidenceID,
		Status:     env.Status,
		Summary:    env.Summary,
	}
}
