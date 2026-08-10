package evidence

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultMatcherDoesNotIncludePaymentRules(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimPaymentMetricAbnormal,
			EvidenceIDs: []string{"ev_1"},
			Subject:     SubjectRef{Service: "payment"},
		},
		[]EvidenceItem{paymentEvidence("ev_1", ObservationMetricAnomaly)},
		nil,
	)
	if report.Status != CoverageUncovered {
		t.Fatalf("expected uncovered for payment claim in default registry, got %#v", report)
	}
}

func TestDefaultMatcherReturnsCoveredForExplicitGenericEvidence(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimLogError,
			EvidenceIDs: []string{"ev_1"},
			Subject:     SubjectRef{Type: "log"},
		},
		[]EvidenceItem{genericEvidence("ev_1", ObservationLogError)},
		nil,
	)
	if report.Status != CoverageCovered {
		t.Fatalf("expected covered, got %#v", report)
	}
	if len(report.MatchedEvidenceIDs) != 1 || report.MatchedEvidenceIDs[0] != "ev_1" {
		t.Fatalf("expected matched evidence id, got %#v", report)
	}
}

func TestPaymentMatcherReturnsCoveredWhenExplicitEvidenceMatches(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimPaymentMetricAbnormal,
			EvidenceIDs: []string{"ev_1"},
			Subject:     SubjectRef{Service: "payment"},
		},
		[]EvidenceItem{paymentEvidence("ev_1", ObservationMetricAnomaly)},
		nil,
	)
	if report.Status != CoverageCovered {
		t.Fatalf("expected covered, got %#v", report)
	}
}

func TestPartialResultCannotCoverCompleteStatusDistribution(t *testing.T) {
	item := paymentEvidence("ev_partial", ObservationStatusDistribution)
	item.ResultMetadata = ResultMetadata{
		ReturnedRows:     20,
		MaxRows:          50,
		Truncated:        true,
		TruncationReason: "max_rows",
	}
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-status-distribution",
			ClaimType:   ClaimPaymentStatusDistribution,
			EvidenceIDs: []string{"ev_partial"},
		},
		[]EvidenceItem{item},
		nil,
	)
	if report.Status != CoveragePartial {
		t.Fatalf("partial result must not cover a complete distribution claim, got %#v", report)
	}
}

func TestMatcherDoesNotScanAllEvidenceWhenClaimHasNoReference(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{ClaimID: "claim-1", ClaimType: ClaimPaymentMetricAbnormal},
		[]EvidenceItem{paymentEvidence("ev_1", ObservationMetricAnomaly)},
		nil,
	)
	if report.Status == CoverageCovered {
		t.Fatalf("claim without evidence reference must not be covered: %#v", report)
	}
	if !hasMissingField(report, "evidence_reference") {
		t.Fatalf("expected evidence_reference gap, got %#v", report.MissingFields)
	}
}

func TestMatcherRejectsUnknownEvidenceReference(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimPaymentMetricAbnormal,
			EvidenceIDs: []string{"ev_missing"},
		},
		nil,
		nil,
	)
	if report.Status == CoverageCovered {
		t.Fatalf("unknown evidence reference must not be covered: %#v", report)
	}
	if !hasMissingField(report, "referenced_evidence") {
		t.Fatalf("expected referenced_evidence gap, got %#v", report.MissingFields)
	}
}

func TestMatcherReturnsUnknownWhenReferencedQueryFailed(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:        "claim-1",
			ClaimType:      ClaimPaymentLogError,
			FailedQueryIDs: []string{"fq_1"},
			Subject:        SubjectRef{Service: "payment"},
		},
		nil,
		[]FailedQuery{{FailedQueryID: "fq_1", ToolName: "search_payment_logs", Status: StatusTimeout}},
	)
	if report.Status != CoverageUnknown {
		t.Fatalf("expected unknown, got %#v", report)
	}
	if len(report.FailedQueryIDs) != 1 || report.FailedQueryIDs[0] != "fq_1" {
		t.Fatalf("expected failed query id, got %#v", report)
	}
}

func TestMatcherReturnsPartialWhenReferencedEvidenceMissesFields(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimCallbackTimeoutRootCause,
			EvidenceIDs: []string{"ev_1", "ev_2"},
			Subject:     SubjectRef{Service: "callback"},
		},
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

func TestStaticCodeEvidenceSupportsStaticClaimWhenAligned(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimImplementationMayCall,
			EvidenceIDs: []string{"ev_code"},
			Subject:     SubjectRef{Service: "payment"},
		},
		[]EvidenceItem{{
			EvidenceID:       "ev_code",
			SourceKind:       SourceStaticCode,
			Repository:       "card_service",
			Revision:         "rev-a",
			VersionAlignment: VersionAligned,
			RuntimeVerified:  false,
			Observations: []Observation{{
				Kind:    ObservationStaticCodeCallSite,
				Subject: SubjectRef{Service: "payment"},
			}},
		}},
		nil,
	)
	if report.Status != CoverageCovered {
		t.Fatalf("expected aligned static code claim to be covered, got %#v", report)
	}
}

func TestStaticCodeEvidenceCannotSupportRuntimeClaim(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimMetricValue,
			EvidenceIDs: []string{"ev_code"},
			Subject:     SubjectRef{Service: "payment"},
		},
		[]EvidenceItem{{
			EvidenceID:   "ev_code",
			SourceKind:   SourceStaticCode,
			Observations: []Observation{{Kind: ObservationMetricValue, Subject: SubjectRef{Service: "payment"}}},
		}},
		nil,
	)
	if report.Status == CoverageCovered {
		t.Fatalf("static code must not cover a runtime claim, got %#v", report)
	}
}

func TestAlignedStaticCodeIsRequiredForCallbackRootCauseAlternative(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimCallbackTimeoutRootCause,
			EvidenceIDs: []string{"ev_metric", "ev_log", "ev_recovery", "ev_code"},
			Subject:     SubjectRef{Service: "callback"},
		},
		[]EvidenceItem{
			paymentEvidence("ev_metric", ObservationMetricAnomaly),
			paymentEvidence("ev_log", ObservationLogError),
			paymentEvidence("ev_recovery", ObservationMetricValue),
			{
				EvidenceID:       "ev_code",
				SourceKind:       SourceStaticCode,
				VersionAlignment: VersionMismatch,
				Observations: []Observation{{
					Kind:    ObservationStaticCodeDeclaration,
					Subject: SubjectRef{Service: "callback"},
				}},
			},
		},
		nil,
	)
	if report.Status == CoverageCovered {
		t.Fatalf("mismatched source revision must not prove root cause, got %#v", report)
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
			ClaimID:     "claim-1",
			ClaimType:   "test_status_claim",
			EvidenceIDs: []string{"ev_1"},
			Subject:     SubjectRef{Service: "payment"},
			Fields:      map[string]any{"status": "SUCCESS"},
		},
		[]EvidenceItem{{
			EvidenceID:      "ev_1",
			SourceKind:      SourceRuntimeDB,
			RuntimeVerified: true,
			ObservedAt:      time.Now().UTC(),
			Subject:         SubjectRef{Service: "payment"},
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

func TestMatcherChecksExplicitClaimFieldsAgainstReferencedObservations(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimPaymentLogError,
			EvidenceIDs: []string{"ev_log"},
			Fields:      map[string]any{"error_code": "ERR_EXPECTED"},
		},
		[]EvidenceItem{{
			EvidenceID:      "ev_log",
			SourceKind:      SourceRuntimeLog,
			RuntimeVerified: true,
			ObservedAt:      time.Now().UTC(),
			Observations: []Observation{{
				Kind:   ObservationLogError,
				Fields: map[string]any{"error_code": "ERR_ACTUAL"},
			}},
		}},
		nil,
	)
	if report.Status != CoverageConflict {
		t.Fatalf("expected claim field conflict, got %#v", report)
	}
}

func TestMatcherRejectsReferencedEvidenceOutsideClaimScope(t *testing.T) {
	report := DefaultClaimRuleRegistry().Match(
		CandidateClaim{
			ClaimID:     "claim-1",
			ClaimType:   ClaimLogError,
			EvidenceIDs: []string{"ev_log"},
			Scope:       EvidenceScope{Fields: map[string]string{"environment": "production"}},
		},
		[]EvidenceItem{{
			EvidenceID:      "ev_log",
			SourceKind:      SourceRuntimeLog,
			RuntimeVerified: true,
			ObservedAt:      time.Now().UTC(),
			Scope:           EvidenceScope{Fields: map[string]string{"environment": "staging"}},
			Observations:    []Observation{{Kind: ObservationLogError}},
		}},
		nil,
	)
	if report.Status == CoverageCovered {
		t.Fatalf("evidence outside the explicit claim scope must not be covered: %#v", report)
	}
}

func TestComposeAnswerSeparatesCoverageSectionsAndLocalesLast(t *testing.T) {
	reports := []CoverageReport{
		{Claim: CandidateClaim{ClaimID: "c1", ClaimType: ClaimPaymentMetricAbnormal, Fields: map[string]any{"status": "failed"}}, Status: CoverageCovered, MatchedEvidenceIDs: []string{"ev_1"}},
		{Claim: CandidateClaim{ClaimID: "c2", ClaimType: ClaimCallbackTimeoutRootCause}, Status: CoveragePartial, MatchedEvidenceIDs: []string{"ev_1", "ev_2"}, MissingFields: []FieldRequirement{{Field: "mechanism_or_runbook"}}},
		{Claim: CandidateClaim{ClaimID: "c3", ClaimType: ClaimPaymentLogError}, Status: CoverageUnknown, FailedQueryIDs: []string{"fq_1"}},
	}

	english := ComposeAnswerForLocale(reports, "en-US")
	chinese := ComposeAnswerForLocale(reports, "zh-CN")
	for _, want := range []string{"Verified facts", "ev_1", "Partially supported findings", "mechanism_or_runbook", "Unknown or failed checks", "fq_1"} {
		if !strings.Contains(english, want) {
			t.Fatalf("expected %q in English answer, got %s", want, english)
		}
	}
	for _, want := range []string{"已验证事实", "部分支持的判断", "未知或失败的检查", "ev_1", "fq_1"} {
		if !strings.Contains(chinese, want) {
			t.Fatalf("expected %q in Chinese answer, got %s", want, chinese)
		}
	}
}

func TestClaimSetValidationRequiresStructuredFields(t *testing.T) {
	valid := ClaimSet{
		SchemaVersion: ClaimSchemaVersion,
		Claims: []CandidateClaim{{
			ClaimID:       "claim-1",
			ClaimType:     ClaimLogError,
			AssertionKind: AssertionFact,
			EvidenceIDs:   []string{"ev_1"},
		}},
	}
	if err := ValidateClaimSet(valid); err != nil {
		t.Fatalf("expected valid claim set, got %v", err)
	}
	invalid := ClaimSet{
		SchemaVersion: valid.SchemaVersion,
		Claims:        append([]CandidateClaim(nil), valid.Claims...),
	}
	invalid.Claims[0].ClaimID = ""
	if err := ValidateClaimSet(invalid); err == nil {
		t.Fatal("expected missing claim id to fail validation")
	}
	invalid = ClaimSet{
		SchemaVersion: valid.SchemaVersion,
		Claims:        append([]CandidateClaim(nil), valid.Claims...),
	}
	invalid.Claims[0].Fields = map[string]any{"covered": true}
	if err := ValidateClaimSet(invalid); err == nil {
		t.Fatal("coverage status must not be accepted as a claim field")
	}
}

func TestHypothesisCannotBeReportedAsCovered(t *testing.T) {
	report := ClaimRuleRegistryWithPayment().Match(
		CandidateClaim{
			ClaimID:       "claim-hypothesis",
			ClaimType:     ClaimPaymentLogError,
			AssertionKind: AssertionHypothesis,
			EvidenceIDs:   []string{"ev_log"},
		},
		[]EvidenceItem{{
			EvidenceID:      "ev_log",
			SourceKind:      SourceRuntimeLog,
			RuntimeVerified: true,
			ObservedAt:      time.Now().UTC(),
			Observations:    []Observation{{Kind: ObservationLogError}},
		}},
		nil,
	)
	if report.Status != CoverageSupported {
		t.Fatalf("expected hypothesis to be supported, got %#v", report)
	}
}

func TestClaimSetRejectsInvalidTypedField(t *testing.T) {
	claimSet := ClaimSet{
		SchemaVersion: ClaimSchemaVersion,
		Claims: []CandidateClaim{{
			ClaimID:       "claim-typed",
			ClaimType:     ClaimPaymentLogError,
			AssertionKind: AssertionFact,
			Fields:        map[string]any{"error_code": 42},
		}},
	}
	if err := ValidateClaimSet(claimSet); err == nil {
		t.Fatal("expected invalid claim field type to be rejected")
	}
}

func TestClaimSchemaRestrictsAssertionByClaimType(t *testing.T) {
	staticClaim := ClaimSet{
		SchemaVersion: ClaimSchemaVersion,
		Claims: []CandidateClaim{{
			ClaimID:       "static-fact",
			ClaimType:     ClaimImplementationContains,
			AssertionKind: AssertionFact,
			EvidenceIDs:   []string{"ev_code"},
		}},
	}
	if err := ValidateClaimSet(staticClaim); err == nil {
		t.Fatal("static implementation claim must not use fact assertion")
	}

	rootCauseFact := ClaimSet{
		SchemaVersion: ClaimSchemaVersion,
		Claims: []CandidateClaim{{
			ClaimID:       "root-cause-fact",
			ClaimType:     ClaimCallbackTimeoutRootCause,
			AssertionKind: AssertionFact,
			EvidenceIDs:   []string{"ev_runtime"},
		}},
	}
	if err := ValidateClaimSet(rootCauseFact); err == nil {
		t.Fatal("root-cause claim must remain a hypothesis or next check")
	}
}

func hasMissingField(report CoverageReport, field string) bool {
	for _, missing := range report.MissingFields {
		if missing.Field == field {
			return true
		}
	}
	return false
}

func paymentEvidence(id string, kind string) EvidenceItem {
	return EvidenceItem{
		EvidenceID:      id,
		ToolName:        "payment_tool",
		SourceKind:      runtimeSourceForObservation(kind),
		RuntimeVerified: true,
		ObservedAt:      time.Now().UTC(),
		Subject:         SubjectRef{Type: "payment", Service: "payment"},
		Observations:    []Observation{{Kind: kind, Subject: SubjectRef{Type: "payment", Service: "payment"}, Summary: kind}},
	}
}

func genericEvidence(id string, kind string) EvidenceItem {
	return EvidenceItem{
		EvidenceID:      id,
		ToolName:        "generic_tool",
		SourceKind:      runtimeSourceForObservation(kind),
		RuntimeVerified: true,
		ObservedAt:      time.Now().UTC(),
		Subject:         SubjectRef{Type: "service", Service: "catalog"},
		Observations:    []Observation{{Kind: kind, Subject: SubjectRef{Type: "service", Service: "catalog"}, Summary: kind}},
	}
}

func runtimeSourceForObservation(kind string) EvidenceSourceKind {
	switch kind {
	case ObservationMetricAnomaly, ObservationMetricValue, ObservationStatusDistribution:
		return SourceRuntimeMetric
	case ObservationLogError:
		return SourceRuntimeLog
	case ObservationOrderState, ObservationStateMismatch, ObservationSlowSQL:
		return SourceRuntimeDB
	default:
		return ""
	}
}
