package eval

import "testing"

func TestValidateForbiddenAssessmentAcceptsExactAffirmativeQuote(t *testing.T) {
	answer := "The missing index is the confirmed root cause."
	truth := []AtomicTruth{{ID: "missing_index_confirmed"}}
	modelAssessments := []modelForbiddenClaimAssessment{{
		ForbiddenID:     "missing_index_confirmed",
		Asserted:        true,
		SupportingQuote: "The missing index is the confirmed root cause",
		Polarity:        "affirmed",
	}}

	ids, assessments := validateForbiddenAssessments(answer, truth, modelAssessments)
	if len(ids) != 1 || ids[0] != "missing_index_confirmed" {
		t.Fatalf("affirmed forbidden claim was not accepted: ids=%v assessments=%#v", ids, assessments)
	}
	if !assessments[0].Accepted || assessments[0].RejectionReason != "" {
		t.Fatalf("unexpected assessment: %#v", assessments[0])
	}
}

func TestValidateForbiddenAssessmentRejectsUnverifiedChineseQuote(t *testing.T) {
	answer := "当前证据无法直接证明是缺索引，缺索引只是待验证假设。"
	truth := []AtomicTruth{{ID: "missing_index_confirmed"}}
	modelAssessments := []modelForbiddenClaimAssessment{{
		ForbiddenID:     "missing_index_confirmed",
		Asserted:        true,
		SupportingQuote: "缺索引",
		Polarity:        "affirmed",
	}}

	ids, assessments := validateForbiddenAssessments(answer, truth, modelAssessments)
	if len(ids) != 0 {
		t.Fatalf("unverified hypothesis was accepted as a forbidden claim: %v", ids)
	}
	if assessments[0].RejectionReason != "quote_context_is_not_affirmative" {
		t.Fatalf("unexpected rejection: %#v", assessments[0])
	}
}

func TestValidateForbiddenAssessmentRejectsQuoteNotInAnswer(t *testing.T) {
	answer := "The cause remains unknown."
	truth := []AtomicTruth{{ID: "missing_index_confirmed"}}
	modelAssessments := []modelForbiddenClaimAssessment{{
		ForbiddenID:     "missing_index_confirmed",
		Asserted:        true,
		SupportingQuote: "The missing index is confirmed",
		Polarity:        "affirmed",
	}}

	ids, assessments := validateForbiddenAssessments(answer, truth, modelAssessments)
	if len(ids) != 0 || assessments[0].RejectionReason != "quote_not_found_in_answer" {
		t.Fatalf("invented quote was not rejected: ids=%v assessment=%#v", ids, assessments[0])
	}
}

func TestValidateForbiddenAssessmentRequiresAffirmedPolarity(t *testing.T) {
	answer := "Missing index is a possible explanation."
	truth := []AtomicTruth{{ID: "missing_index_confirmed"}}
	modelAssessments := []modelForbiddenClaimAssessment{{
		ForbiddenID:     "missing_index_confirmed",
		Asserted:        true,
		SupportingQuote: "Missing index is a possible explanation",
		Polarity:        "hypothetical",
	}}

	ids, assessments := validateForbiddenAssessments(answer, truth, modelAssessments)
	if len(ids) != 0 || assessments[0].RejectionReason != "non_affirmative_polarity" {
		t.Fatalf("hypothetical claim was not rejected: ids=%v assessment=%#v", ids, assessments[0])
	}
}

func TestValidateForbiddenAssessmentRecordsMissingModelDecision(t *testing.T) {
	truth := []AtomicTruth{{ID: "missing_index_confirmed"}}
	ids, assessments := validateForbiddenAssessments("No index conclusion.", truth, nil)
	if len(ids) != 0 || len(assessments) != 1 {
		t.Fatalf("unexpected missing-decision result: ids=%v assessments=%#v", ids, assessments)
	}
	if assessments[0].Polarity != "not_mentioned" || assessments[0].RejectionReason != "missing_model_assessment" {
		t.Fatalf("missing decision is not auditable: %#v", assessments[0])
	}
}

func TestDeterministicNormalizerDoesNotFlagUnverifiedForbiddenClaim(t *testing.T) {
	evalCase := Case{Truth: &AblationTruth{
		ForbiddenClaims: []AtomicTruth{{
			ID:          "missing_index_confirmed",
			Description: "a missing index is confirmed",
			MatchAny:    []string{"missing index is confirmed"},
			Critical:    true,
		}},
	}}
	result := EvalCaseResult{Answer: "A missing index is not confirmed and remains unverified."}

	normalized := normalizeAblationClaims(evalCase, result)
	if len(normalized.ForbiddenIDs) != 0 {
		t.Fatalf("fallback normalizer produced a false positive: %#v", normalized)
	}
	if len(normalized.ForbiddenAssessments) != 1 || normalized.ForbiddenAssessments[0].Accepted {
		t.Fatalf("fallback decision is not auditable: %#v", normalized.ForbiddenAssessments)
	}
}
