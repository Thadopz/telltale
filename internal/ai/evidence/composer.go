package evidence

import (
	"strings"
)

func ExtractCandidateClaims(answer string) []CandidateClaim {
	normalized := strings.ToLower(answer)
	claims := make([]CandidateClaim, 0, 8)
	if containsAny(normalized, "metric", "latency", "success rate", "error rate", "traffic", "qps", "cpu", "memory", "prometheus") ||
		containsAny(normalized, "abnormal", "failure", "fail", "degraded", "incident") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimMetricAnomaly})
	}
	if containsAny(normalized, "metric value", "current value", "rate", "p95", "p99", "throughput") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimMetricValue})
	}
	if containsAny(normalized, "log", "error", "exception", "timeout", "timed out") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimLogError, Subject: SubjectRef{Type: "log"}})
	}
	if containsAny(normalized, "status distribution", "distribution", "impact scope", "blast radius") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimStatusDistribution})
	}
	if containsAny(normalized, "state mismatch", "mismatch", "inconsistent", "consistency") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimStateMismatch})
	}
	if containsAny(normalized, "slow sql", "sql latency", "rows examined") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimSlowSQL, Subject: SubjectRef{Type: "database"}})
	}
	if containsAny(normalized, "runbook", "mechanism", "rule") {
		claims = append(claims, CandidateClaim{ClaimType: ClaimRunbookRule})
	}
	if textHasPaymentSignal(normalized) {
		claims = appendPaymentCandidateClaims(claims, normalized)
	}
	return dedupeClaims(claims)
}

func appendPaymentCandidateClaims(claims []CandidateClaim, normalized string) []CandidateClaim {
	if containsAny(normalized, "callback") && containsAny(normalized, "root cause", "confirmed") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimCallbackTimeoutRootCause,
			Subject:   SubjectRef{Type: "service", Service: "callback"},
			Fields:    map[string]any{"symptom": "payment_failure"},
		})
	} else if containsAny(normalized, "callback") && containsAny(normalized, "timeout", "timed out") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimCallbackTimeoutRelated,
			Subject:   SubjectRef{Type: "service", Service: "callback"},
			Fields:    map[string]any{"symptom": "payment_failure"},
		})
	}
	if containsAny(normalized, "fail", "failure", "abnormal") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimPaymentMetricAbnormal,
			Subject:   SubjectRef{Type: "payment", Service: "payment"},
		})
	}
	if containsAny(normalized, "order state", "order status", "payment state", "status") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimPaymentOrderState,
			Subject:   SubjectRef{Type: "payment", Service: "payment"},
		})
	}
	if containsAny(normalized, "mismatch", "inconsistent", "consistency") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimPaymentStateMismatch,
			Subject:   SubjectRef{Type: "payment", Service: "payment"},
		})
	}
	if containsAny(normalized, "log", "error") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimPaymentLogError,
			Subject:   SubjectRef{Type: "log", Service: "payment"},
		})
	}
	if containsAny(normalized, "status distribution", "distribution", "impact scope", "blast radius") {
		claims = append(claims, CandidateClaim{
			ClaimType: ClaimPaymentStatusDistribution,
			Subject:   SubjectRef{Type: "payment", Service: "payment"},
		})
	}
	return claims
}

func ComposeAnswer(reports []CoverageReport) string {
	if len(reports) == 0 {
		return ""
	}
	var covered, partial, unknown, uncovered, conflict []CoverageReport
	for _, report := range reports {
		switch report.Status {
		case CoverageCovered:
			covered = append(covered, report)
		case CoveragePartial:
			partial = append(partial, report)
		case CoverageUnknown:
			unknown = append(unknown, report)
		case CoverageConflict:
			conflict = append(conflict, report)
		default:
			uncovered = append(uncovered, report)
		}
	}

	var b strings.Builder
	writeReportSection(&b, "Verified evidence", covered, "covered")
	writeReportSection(&b, "Possible related findings", partial, "partial")
	writeReportSection(&b, "Unknown or failed queries", unknown, "unknown")
	writeReportSection(&b, "Evidence conflicts", conflict, "conflict")
	writeReportSection(&b, "Evidence gaps", uncovered, "uncovered")
	b.WriteString("\nNext steps:\n")
	b.WriteString("- Re-run or add targeted tools for missing fields before confirming stronger conclusions.\n")
	return strings.TrimSpace(b.String())
}

func writeReportSection(b *strings.Builder, title string, reports []CoverageReport, mode string) {
	if len(reports) == 0 {
		return
	}
	b.WriteString(title)
	b.WriteString(":\n")
	for _, report := range reports {
		b.WriteString("- ")
		b.WriteString(report.Claim.ClaimType)
		switch mode {
		case "covered":
			b.WriteString(" is covered by ")
			b.WriteString(strings.Join(report.MatchedEvidenceIDs, ", "))
		case "partial":
			b.WriteString(" is only partially covered by ")
			b.WriteString(strings.Join(report.MatchedEvidenceIDs, ", "))
			if len(report.MissingFields) > 0 {
				b.WriteString("; missing ")
				b.WriteString(requirementNames(report.MissingFields))
			}
		case "unknown":
			b.WriteString(" is unknown because related queries failed")
			if len(report.FailedQueryIDs) > 0 {
				b.WriteString(": ")
				b.WriteString(strings.Join(report.FailedQueryIDs, ", "))
			}
		case "conflict":
			b.WriteString(" has conflicting evidence")
		default:
			b.WriteString(" is not covered by current evidence")
			if len(report.MissingFields) > 0 {
				b.WriteString("; missing ")
				b.WriteString(requirementNames(report.MissingFields))
			}
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func requirementNames(requirements []FieldRequirement) string {
	names := make([]string, 0, len(requirements))
	for _, req := range requirements {
		names = append(names, req.Field)
	}
	return strings.Join(names, ", ")
}

func dedupeClaims(claims []CandidateClaim) []CandidateClaim {
	seen := map[string]bool{}
	out := make([]CandidateClaim, 0, len(claims))
	for _, claim := range claims {
		if !ValidClaimType(claim.ClaimType) || seen[claim.ClaimType] {
			continue
		}
		seen[claim.ClaimType] = true
		out = append(out, claim)
	}
	return out
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
