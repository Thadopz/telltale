package evidence

import (
	"fmt"
	"strings"
)

type FieldRequirement struct {
	Field            string   `json:"field"`
	ObservationKinds []string `json:"observation_kinds,omitempty"`
	ClaimField       string   `json:"claim_field,omitempty"`
}

type ConflictCheck struct {
	Field      string `json:"field"`
	ClaimField string `json:"claim_field,omitempty"`
}

type ClaimRule struct {
	ClaimType      string             `json:"claim_type"`
	Required       []FieldRequirement `json:"required"`
	Optional       []FieldRequirement `json:"optional,omitempty"`
	ConflictChecks []ConflictCheck    `json:"conflict_checks,omitempty"`
}

type FieldMatch struct {
	Field      string `json:"field"`
	EvidenceID string `json:"evidence_id"`
	ToolName   string `json:"tool_name,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type FieldConflict struct {
	Field      string `json:"field"`
	EvidenceID string `json:"evidence_id"`
	Expected   string `json:"expected,omitempty"`
	Actual     string `json:"actual,omitempty"`
}

type CoverageReport struct {
	Claim              CandidateClaim     `json:"claim"`
	Status             string             `json:"status"`
	MatchedFields      []FieldMatch       `json:"matched_fields,omitempty"`
	MissingFields      []FieldRequirement `json:"missing_fields,omitempty"`
	ConflictFields     []FieldConflict    `json:"conflict_fields,omitempty"`
	MatchedEvidenceIDs []string           `json:"matched_evidence_ids,omitempty"`
	RelatedEvidenceIDs []string           `json:"related_evidence_ids,omitempty"`
	FailedQueryIDs     []string           `json:"failed_query_ids,omitempty"`
	RuleName           string             `json:"rule_name,omitempty"`
}

type ClaimRuleRegistry struct {
	rules map[string]ClaimRule
}

func NewClaimRuleRegistry(rules map[string]ClaimRule) *ClaimRuleRegistry {
	copied := make(map[string]ClaimRule, len(rules))
	for key, rule := range rules {
		copied[key] = rule
	}
	return &ClaimRuleRegistry{rules: copied}
}

func DefaultClaimRuleRegistry() *ClaimRuleRegistry {
	return NewClaimRuleRegistry(DefaultClaimRules())
}

func ClaimRuleRegistryWithPayment() *ClaimRuleRegistry {
	return NewClaimRuleRegistry(ClaimRulesWithPayment())
}

func DefaultClaimRules() map[string]ClaimRule {
	return map[string]ClaimRule{
		ClaimMetricAnomaly: {
			ClaimType: ClaimMetricAnomaly,
			Required:  []FieldRequirement{{Field: "metric_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}}},
		},
		ClaimMetricValue: {
			ClaimType: ClaimMetricValue,
			Required:  []FieldRequirement{{Field: "metric_value", ObservationKinds: []string{ObservationMetricValue}}},
		},
		ClaimLogError: {
			ClaimType: ClaimLogError,
			Required:  []FieldRequirement{{Field: "log_error", ObservationKinds: []string{ObservationLogError}}},
		},
		ClaimStatusDistribution: {
			ClaimType: ClaimStatusDistribution,
			Required:  []FieldRequirement{{Field: "status_distribution", ObservationKinds: []string{ObservationStatusDistribution}}},
		},
		ClaimStateMismatch: {
			ClaimType: ClaimStateMismatch,
			Required:  []FieldRequirement{{Field: "state_mismatch", ObservationKinds: []string{ObservationStateMismatch}}},
		},
		ClaimSlowSQL: {
			ClaimType: ClaimSlowSQL,
			Required:  []FieldRequirement{{Field: "slow_sql", ObservationKinds: []string{ObservationSlowSQL}}},
		},
		ClaimRunbookRule: {
			ClaimType: ClaimRunbookRule,
			Required:  []FieldRequirement{{Field: "runbook_rule", ObservationKinds: []string{ObservationRunbookRule}}},
		},
	}
}

func PaymentClaimRules() map[string]ClaimRule {
	return map[string]ClaimRule{
		ClaimPaymentMetricAbnormal: {
			ClaimType: ClaimPaymentMetricAbnormal,
			Required:  []FieldRequirement{{Field: "payment_metric_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}}},
		},
		ClaimPaymentOrderState: {
			ClaimType: ClaimPaymentOrderState,
			Required:  []FieldRequirement{{Field: "payment_order_state", ObservationKinds: []string{ObservationOrderState}}},
		},
		ClaimPaymentStateMismatch: {
			ClaimType: ClaimPaymentStateMismatch,
			Required:  []FieldRequirement{{Field: "payment_state_mismatch", ObservationKinds: []string{ObservationStateMismatch}}},
		},
		ClaimPaymentLogError: {
			ClaimType: ClaimPaymentLogError,
			Required:  []FieldRequirement{{Field: "payment_log_error", ObservationKinds: []string{ObservationLogError}}},
		},
		ClaimPaymentStatusDistribution: {
			ClaimType: ClaimPaymentStatusDistribution,
			Required:  []FieldRequirement{{Field: "payment_status_distribution", ObservationKinds: []string{ObservationStatusDistribution}}},
		},
		ClaimCallbackTimeoutRelated: {
			ClaimType: ClaimCallbackTimeoutRelated,
			Required: []FieldRequirement{
				{Field: "payment_failure_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}},
				{Field: "callback_timeout_log", ObservationKinds: []string{ObservationLogError}},
			},
		},
		ClaimCallbackTimeoutRootCause: {
			ClaimType: ClaimCallbackTimeoutRootCause,
			Required: []FieldRequirement{
				{Field: "payment_failure_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}},
				{Field: "callback_timeout_log", ObservationKinds: []string{ObservationLogError}},
				{Field: "mechanism_or_runbook", ObservationKinds: []string{ObservationRunbookRule}},
				{Field: "recovery_or_exclusion_evidence", ObservationKinds: []string{ObservationStatusDistribution, ObservationMetricValue}},
			},
		},
	}
}

func ClaimRulesWithPayment() map[string]ClaimRule {
	rules := DefaultClaimRules()
	for key, rule := range PaymentClaimRules() {
		rules[key] = rule
	}
	return rules
}

func IsPaymentClaimType(claimType string) bool {
	_, ok := PaymentClaimRules()[strings.TrimSpace(claimType)]
	return ok
}

func ShouldUsePaymentClaimRules(claims []CandidateClaim, evidenceItems []EvidenceItem, failedQueries []FailedQuery) bool {
	for _, claim := range claims {
		if IsPaymentClaimType(claim.ClaimType) || subjectHasPaymentSignal(claim.Subject) || fieldsHavePaymentSignal(claim.Fields) {
			return true
		}
	}
	for _, item := range evidenceItems {
		if toolNameHasPaymentSignal(item.ToolName) || subjectHasPaymentSignal(item.Subject) {
			return true
		}
		for _, obs := range item.Observations {
			if subjectHasPaymentSignal(obs.Subject) || fieldsHavePaymentSignal(obs.Fields) {
				return true
			}
		}
	}
	for _, query := range failedQueries {
		if toolNameHasPaymentSignal(query.ToolName) || textHasPaymentSignal(query.Summary) {
			return true
		}
	}
	return false
}

func (r *ClaimRuleRegistry) Match(claim CandidateClaim, evidenceItems []EvidenceItem, failedQueries []FailedQuery) CoverageReport {
	rule, ok := r.rules[strings.TrimSpace(claim.ClaimType)]
	report := CoverageReport{
		Claim:    claim,
		RuleName: claim.ClaimType,
	}
	if !ok {
		report.Status = CoverageUncovered
		report.MissingFields = []FieldRequirement{{Field: "registered_claim_rule"}}
		return report
	}

	related := relatedEvidence(claim, evidenceItems)
	report.RelatedEvidenceIDs = evidenceIDs(related)
	if len(related) == 0 {
		report.FailedQueryIDs = failedQueryIDs(failedQueries)
		if len(report.FailedQueryIDs) > 0 {
			report.Status = CoverageUnknown
		} else {
			report.Status = CoverageUncovered
		}
		report.MissingFields = append(report.MissingFields, rule.Required...)
		return report
	}

	for _, req := range rule.Required {
		match, ok := findRequirementMatch(req, related)
		if ok {
			report.MatchedFields = append(report.MatchedFields, match)
			report.MatchedEvidenceIDs = appendUnique(report.MatchedEvidenceIDs, match.EvidenceID)
		} else {
			report.MissingFields = append(report.MissingFields, req)
		}
	}

	report.ConflictFields = findConflicts(rule.ConflictChecks, claim, related)
	if len(report.ConflictFields) > 0 {
		report.Status = CoverageConflict
		return report
	}
	if len(report.MissingFields) == 0 {
		report.Status = CoverageCovered
		return report
	}
	report.Status = CoveragePartial
	return report
}

func relatedEvidence(claim CandidateClaim, evidenceItems []EvidenceItem) []EvidenceItem {
	out := make([]EvidenceItem, 0, len(evidenceItems))
	for _, item := range evidenceItems {
		if strings.TrimSpace(item.EvidenceID) == "" || len(item.Observations) == 0 {
			continue
		}
		if isCrossSubjectPaymentClaim(claim.ClaimType) {
			out = append(out, item)
			continue
		}
		if subjectRelated(claim.Subject, item.Subject) || observationsRelated(claim.Subject, item.Observations) || claim.Subject.Service == "" {
			out = append(out, item)
		}
	}
	return out
}

func isCrossSubjectPaymentClaim(claimType string) bool {
	return claimType == ClaimCallbackTimeoutRelated || claimType == ClaimCallbackTimeoutRootCause
}

func observationsRelated(subject SubjectRef, observations []Observation) bool {
	for _, obs := range observations {
		if subjectRelated(subject, obs.Subject) {
			return true
		}
	}
	return false
}

func subjectRelated(claimSubject SubjectRef, evidenceSubject SubjectRef) bool {
	if claimSubject.Service != "" && evidenceSubject.Service != "" && !strings.EqualFold(claimSubject.Service, evidenceSubject.Service) {
		return false
	}
	if claimSubject.OrderNo != "" && evidenceSubject.OrderNo != "" && !strings.EqualFold(claimSubject.OrderNo, evidenceSubject.OrderNo) {
		return false
	}
	if claimSubject.TraceID != "" && evidenceSubject.TraceID != "" && !strings.EqualFold(claimSubject.TraceID, evidenceSubject.TraceID) {
		return false
	}
	return true
}

func findRequirementMatch(req FieldRequirement, evidenceItems []EvidenceItem) (FieldMatch, bool) {
	for _, item := range evidenceItems {
		for _, obs := range item.Observations {
			if kindAllowed(obs.Kind, req.ObservationKinds) {
				return FieldMatch{Field: req.Field, EvidenceID: item.EvidenceID, ToolName: item.ToolName, Summary: obs.Summary}, true
			}
		}
	}
	return FieldMatch{}, false
}

func kindAllowed(kind string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(kind, candidate) {
			return true
		}
	}
	return false
}

func findConflicts(checks []ConflictCheck, claim CandidateClaim, evidenceItems []EvidenceItem) []FieldConflict {
	var conflicts []FieldConflict
	for _, check := range checks {
		expected := strings.TrimSpace(toString(claim.Fields[check.ClaimField]))
		if expected == "" {
			expected = strings.TrimSpace(toString(claim.Fields[check.Field]))
		}
		if expected == "" {
			continue
		}
		for _, item := range evidenceItems {
			for _, obs := range item.Observations {
				actual := strings.TrimSpace(toString(obs.Fields[check.Field]))
				if actual != "" && !strings.EqualFold(actual, expected) {
					conflicts = append(conflicts, FieldConflict{Field: check.Field, EvidenceID: item.EvidenceID, Expected: expected, Actual: actual})
				}
			}
		}
	}
	return conflicts
}

func evidenceIDs(items []EvidenceItem) []string {
	var ids []string
	for _, item := range items {
		ids = appendUnique(ids, item.EvidenceID)
	}
	return ids
}

func failedQueryIDs(items []FailedQuery) []string {
	var ids []string
	for _, item := range items {
		ids = appendUnique(ids, item.FailedQueryID)
	}
	return ids
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func subjectHasPaymentSignal(subject SubjectRef) bool {
	return textHasPaymentSignal(subject.Type) ||
		textHasPaymentSignal(subject.Service) ||
		strings.TrimSpace(subject.OrderNo) != "" ||
		mapStringHasPaymentSignal(subject.Fields)
}

func fieldsHavePaymentSignal(fields map[string]any) bool {
	for key, value := range fields {
		if textHasPaymentSignal(key) || textHasPaymentSignal(toString(value)) {
			return true
		}
	}
	return false
}

func mapStringHasPaymentSignal(fields map[string]string) bool {
	for key, value := range fields {
		if textHasPaymentSignal(key) || textHasPaymentSignal(value) {
			return true
		}
	}
	return false
}

func toolNameHasPaymentSignal(toolName string) bool {
	return textHasPaymentSignal(toolName)
}

func textHasPaymentSignal(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	markers := []string{
		"payment",
		"refund",
		"callback",
		"notify",
		"payment_order",
		"payment order",
		"order_no",
		"order no",
		"order id",
		"支付",
		"退款",
		"回调",
		"通知",
		"订单号",
		"支付订单",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
