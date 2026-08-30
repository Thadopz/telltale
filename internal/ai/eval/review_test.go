package eval

import "testing"

func TestCalculateReviewerAgreementExpandsReviewWhenKappaIsLow(t *testing.T) {
	results := []EvalCaseResult{
		{EvidenceMode: EvidenceModeFull, ReviewID: "r1", Judge: JudgeResult{Enabled: true, Pass: true}},
		{EvidenceMode: EvidenceModeFull, ReviewID: "r2", Judge: JudgeResult{Enabled: true, Pass: true}},
		{EvidenceMode: EvidenceModeRawUnenforced, ReviewID: "r3", Judge: JudgeResult{Enabled: true, Pass: false}},
		{EvidenceMode: EvidenceModeRawUnenforced, ReviewID: "r4", Judge: JudgeResult{Enabled: true, Pass: false}},
	}
	reviews := []HumanReview{
		{ReviewID: "r1", CaseCorrect: false},
		{ReviewID: "r2", CaseCorrect: false},
		{ReviewID: "r3", CaseCorrect: true},
		{ReviewID: "r4", CaseCorrect: true},
	}
	agreement := CalculateReviewerAgreement(results, reviews)
	if agreement.Reliable || agreement.CohensKappa >= 0.7 {
		t.Fatalf("expected low agreement: %#v", agreement)
	}
	if agreement.RecommendedReviewFraction != 0.25 {
		t.Fatalf("expected review expansion to 25 percent: %#v", agreement)
	}
}

func TestCalculateReviewerAgreementKeepsTenPercentWhenReliable(t *testing.T) {
	results := []EvalCaseResult{
		{EvidenceMode: EvidenceModeFull, ReviewID: "r1", Judge: JudgeResult{Enabled: true, Pass: true}},
		{EvidenceMode: EvidenceModeRawUnenforced, ReviewID: "r2", Judge: JudgeResult{Enabled: true, Pass: false}},
	}
	reviews := []HumanReview{{ReviewID: "r1", CaseCorrect: true}, {ReviewID: "r2", CaseCorrect: false}}
	agreement := CalculateReviewerAgreement(results, reviews)
	if !agreement.Reliable || agreement.CohensKappa != 1 || agreement.RecommendedReviewFraction != 0.10 {
		t.Fatalf("unexpected reliable agreement: %#v", agreement)
	}
}
