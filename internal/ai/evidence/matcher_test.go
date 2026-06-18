package evidence

import (
	"strings"
	"testing"
)

func TestDefaultMatcherDoesNotIncludePaymentRules(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{ClaimType: ClaimPaymentMetricAbnormal, Subject: SubjectRef{Service: "payment"}},
		[]EvidenceItem{paymentEvidence("ev_1", ObservationMetricAnomaly)},
		nil,
	)
	if report.Status != CoverageUncovered {
		t.Fatalf("expected uncovered for payment claim in default registry, got %#v", report)
	}
	if len(report.MissingFields) != 1 || report.MissingFields[0].Field != "registered_claim_rule" {
		t.Fatalf("expected missing registered_claim_rule, got %#v", report.MissingFields)
	}
}

func TestDefaultMatcherReturnsCoveredForGenericLogError(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{ClaimType: ClaimLogError, Subject: SubjectRef{Type: "log"}},
		[]EvidenceItem{genericEvidence("ev_1", ObservationLogError)},
		nil,
	)
	if report.Status != CoverageCovered {
		t.Fatalf("expected covered, got %#v", report)
	}
	if len(report.MatchedEvidenceIDs) != 1 || report.MatchedEvidenceIDs[0] != "ev_1" {
		t.Fatalf("expected matched evidence id, got %#v", report.MatchedEvidenceIDs)
	}
}

func TestPaymentMatcherReturnsCoveredWhenRequiredFieldsMatch(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{ClaimType: ClaimPaymentMetricAbnormal, Subject: SubjectRef{Service: "payment"}},
		[]EvidenceItem{paymentEvidence("ev_1", ObservationMetricAnomaly)},
		nil,
	)
	if report.Status != CoverageCovered {
		t.Fatalf("expected covered, got %#v", report)
	}
	if len(report.MatchedEvidenceIDs) != 1 || report.MatchedEvidenceIDs[0] != "ev_1" {
		t.Fatalf("expected matched evidence id, got %#v", report.MatchedEvidenceIDs)
	}
}

func TestMatcherReturnsPartialWhenRelatedEvidenceMissesRequiredFields(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{ClaimType: ClaimCallbackTimeoutRootCause, Subject: SubjectRef{Service: "callback"}},
		[]EvidenceItem{
			paymentEvidence("ev_1", ObservationMetricAnomaly),
			paymentEvidence("ev_2", ObservationLogError),
		},
		nil,
	)
	if report.Status != CoveragePartial {
		t.Fatalf("expected partial, got %#v", report)
	}
	if len(report.MissingFields) != 2 {
		t.Fatalf("expected missing mechanism and recovery fields, got %#v", report.MissingFields)
	}
}

func TestMatcherReturnsUnknownWhenOnlyFailedQueriesExist(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{ClaimType: ClaimPaymentLogError, Subject: SubjectRef{Service: "payment"}},
		nil,
		[]FailedQuery{{FailedQueryID: "fq_1", ToolName: "search_payment_logs", Status: StatusTimeout}},
	)
	if report.Status != CoverageUnknown {
		t.Fatalf("expected unknown, got %#v", report)
	}
	if len(report.FailedQueryIDs) != 1 || report.FailedQueryIDs[0] != "fq_1" {
		t.Fatalf("expected failed query id, got %#v", report.FailedQueryIDs)
	}
}

func TestMatcherReturnsUncoveredWithoutEvidence(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{ClaimType: ClaimPaymentStatusDistribution, Subject: SubjectRef{Service: "payment"}},
		nil,
		nil,
	)
	if report.Status != CoverageUncovered {
		t.Fatalf("expected uncovered, got %#v", report)
	}
}

func TestMatcherReturnsConflictWhenFieldValuesDisagree(t *testing.T) {
	registry := NewClaimRuleRegistry(map[string]ClaimRule{
		"test_status_claim": {
			ClaimType:      "test_status_claim",
			Required:       []FieldRequirement{{Field: "order_state", ObservationKinds: []string{ObservationOrderState}}},
			ConflictChecks: []ConflictCheck{{Field: "status"}},
		},
	})
	report := registry.Match(
		CandidateClaim{
			ClaimType: "test_status_claim",
			Subject:   SubjectRef{Service: "payment"},
			Fields:    map[string]any{"status": "SUCCESS"},
		},
		[]EvidenceItem{{
			EvidenceID: "ev_1",
			ToolName:   "query_payment_order_detail",
			Subject:    SubjectRef{Service: "payment"},
			Observations: []Observation{{
				Kind:    ObservationOrderState,
				Subject: SubjectRef{Service: "payment"},
				Fields:  map[string]any{"status": "FAILED"},
			}},
		}},
		nil,
	)
	if report.Status != CoverageConflict {
		t.Fatalf("expected conflict, got %#v", report)
	}
}

func TestComposeAnswerSeparatesCoverageSections(t *testing.T) {
	reports := []CoverageReport{
		{
			Claim:              CandidateClaim{ClaimType: ClaimPaymentMetricAbnormal},
			Status:             CoverageCovered,
			MatchedEvidenceIDs: []string{"ev_1"},
		},
		{
			Claim:              CandidateClaim{ClaimType: ClaimCallbackTimeoutRootCause},
			Status:             CoveragePartial,
			MatchedEvidenceIDs: []string{"ev_1", "ev_2"},
			MissingFields:      []FieldRequirement{{Field: "mechanism_or_runbook"}},
		},
		{
			Claim:          CandidateClaim{ClaimType: ClaimPaymentLogError},
			Status:         CoverageUnknown,
			FailedQueryIDs: []string{"fq_1"},
		},
	}
	got := ComposeAnswer(reports)
	for _, want := range []string{"Verified evidence", "ev_1", "Possible related findings", "mechanism_or_runbook", "Unknown or failed queries", "fq_1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in composed answer, got %s", want, got)
		}
	}
}

func TestExtractCandidateClaimsUsesGenericClaimsWithoutPaymentSignal(t *testing.T) {
	claims := ExtractCandidateClaims("product-catalog has error logs and an incident")
	if !hasClaim(claims, ClaimLogError) {
		t.Fatalf("expected generic log error claim, got %#v", claims)
	}
	if hasClaim(claims, ClaimPaymentLogError) {
		t.Fatalf("did not expect payment log claim, got %#v", claims)
	}
}

func TestExtractCandidateClaimsAddsPaymentClaimsForPaymentSignal(t *testing.T) {
	claims := ExtractCandidateClaims("payment failure has error logs")
	if !hasClaim(claims, ClaimPaymentMetricAbnormal) || !hasClaim(claims, ClaimPaymentLogError) {
		t.Fatalf("expected payment claims, got %#v", claims)
	}
	if !hasClaim(claims, ClaimMetricAnomaly) || !hasClaim(claims, ClaimLogError) {
		t.Fatalf("expected generic claims to remain, got %#v", claims)
	}
}

func paymentEvidence(id string, kind string) EvidenceItem {
	return EvidenceItem{
		EvidenceID: id,
		ToolName:   "payment_tool",
		Subject:    SubjectRef{Type: "payment", Service: "payment"},
		Observations: []Observation{{
			Kind:    kind,
			Subject: SubjectRef{Type: "payment", Service: "payment"},
			Summary: kind,
		}},
	}
}

func genericEvidence(id string, kind string) EvidenceItem {
	return EvidenceItem{
		EvidenceID: id,
		ToolName:   "generic_tool",
		Subject:    SubjectRef{Type: "service", Service: "catalog"},
		Observations: []Observation{{
			Kind:    kind,
			Subject: SubjectRef{Type: "service", Service: "catalog"},
			Summary: kind,
		}},
	}
}

func hasClaim(claims []CandidateClaim, claimType string) bool {
	for _, claim := range claims {
		if claim.ClaimType == claimType {
			return true
		}
	}
	return false
}
