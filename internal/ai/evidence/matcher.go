package evidence

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type FieldRequirement struct {
	Field               string               `json:"field"`
	ObservationKinds    []string             `json:"observation_kinds,omitempty"`
	ClaimField          string               `json:"claim_field,omitempty"`
	AllowedSourceKinds  []EvidenceSourceKind `json:"allowed_source_kinds,omitempty"`
	RequireRuntime      bool                 `json:"require_runtime_verified,omitempty"`
	RequireVersionMatch bool                 `json:"require_version_alignment,omitempty"`
}

type ConflictCheck struct {
	Field      string `json:"field"`
	ClaimField string `json:"claim_field,omitempty"`
}

type ClaimRule struct {
	ClaimType              string             `json:"claim_type"`
	Required               []FieldRequirement `json:"required"`
	AlternativeRequired    []FieldRequirement `json:"alternative_required,omitempty"`
	Optional               []FieldRequirement `json:"optional,omitempty"`
	ConflictChecks         []ConflictCheck    `json:"conflict_checks,omitempty"`
	RequiresCompleteResult bool               `json:"requires_complete_result,omitempty"`
}

type FieldMatch struct {
	Field            string             `json:"field"`
	EvidenceID       string             `json:"evidence_id"`
	ToolName         string             `json:"tool_name,omitempty"`
	SourceKind       EvidenceSourceKind `json:"source_kind,omitempty"`
	Revision         string             `json:"revision,omitempty"`
	VersionAlignment VersionAlignment   `json:"version_alignment,omitempty"`
	RuntimeVerified  bool               `json:"runtime_verified"`
	Summary          string             `json:"summary,omitempty"`
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
	EvidenceConflicts  []EvidenceConflict `json:"evidence_conflicts,omitempty"`
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
			Required:  []FieldRequirement{{Field: "metric_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true}},
		},
		ClaimMetricValue: {
			ClaimType: ClaimMetricValue,
			Required:  []FieldRequirement{{Field: "metric_value", ObservationKinds: []string{ObservationMetricValue}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true}},
		},
		ClaimLogError: {
			ClaimType: ClaimLogError,
			Required:  []FieldRequirement{{Field: "log_error", ObservationKinds: []string{ObservationLogError}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeLog}, RequireRuntime: true}},
		},
		ClaimStatusDistribution: {
			ClaimType:              ClaimStatusDistribution,
			Required:               []FieldRequirement{{Field: "status_distribution", ObservationKinds: []string{ObservationStatusDistribution}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true}},
			RequiresCompleteResult: true,
		},
		ClaimStateMismatch: {
			ClaimType: ClaimStateMismatch,
			Required:  []FieldRequirement{{Field: "state_mismatch", ObservationKinds: []string{ObservationStateMismatch}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeDB}, RequireRuntime: true}},
		},
		ClaimSlowSQL: {
			ClaimType: ClaimSlowSQL,
			Required:  []FieldRequirement{{Field: "slow_sql", ObservationKinds: []string{ObservationSlowSQL}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeDB}, RequireRuntime: true}},
		},
		ClaimRunbookRule: {
			ClaimType: ClaimRunbookRule,
			Required:  []FieldRequirement{{Field: "runbook_rule", ObservationKinds: []string{ObservationRunbookRule}}},
		},
		ClaimImplementationContains: {
			ClaimType: ClaimImplementationContains,
			Required: []FieldRequirement{{
				Field:              "implementation_contains",
				ObservationKinds:   []string{ObservationStaticCodeDeclaration, ObservationStaticConfig},
				AllowedSourceKinds: []EvidenceSourceKind{SourceStaticCode, SourceStaticConfig},
			}},
		},
		ClaimImplementationMayCall: {
			ClaimType: ClaimImplementationMayCall,
			Required: []FieldRequirement{{
				Field:              "implementation_may_call",
				ObservationKinds:   []string{ObservationStaticCodeCallSite},
				AllowedSourceKinds: []EvidenceSourceKind{SourceStaticCode},
			}},
		},
		ClaimConfigValueDefined: {
			ClaimType: ClaimConfigValueDefined,
			Required: []FieldRequirement{{
				Field:              "config_value_defined",
				ObservationKinds:   []string{ObservationStaticConfig},
				AllowedSourceKinds: []EvidenceSourceKind{SourceStaticConfig},
			}},
		},
		ClaimErrorMappedAtCodeLocation: {
			ClaimType: ClaimErrorMappedAtCodeLocation,
			Required: []FieldRequirement{{
				Field:              "error_mapped_at_code_location",
				ObservationKinds:   []string{ObservationStaticCodeDeclaration},
				AllowedSourceKinds: []EvidenceSourceKind{SourceStaticCode},
			}},
		},
	}
}

func PaymentClaimRules() map[string]ClaimRule {
	return map[string]ClaimRule{
		ClaimPaymentMetricAbnormal: {
			ClaimType: ClaimPaymentMetricAbnormal,
			Required:  []FieldRequirement{{Field: "payment_metric_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true}},
		},
		ClaimPaymentOrderState: {
			ClaimType: ClaimPaymentOrderState,
			Required:  []FieldRequirement{{Field: "payment_order_state", ObservationKinds: []string{ObservationOrderState}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeDB}, RequireRuntime: true}},
		},
		ClaimPaymentStateMismatch: {
			ClaimType: ClaimPaymentStateMismatch,
			Required:  []FieldRequirement{{Field: "payment_state_mismatch", ObservationKinds: []string{ObservationStateMismatch}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeDB}, RequireRuntime: true}},
		},
		ClaimPaymentLogError: {
			ClaimType: ClaimPaymentLogError,
			Required:  []FieldRequirement{{Field: "payment_log_error", ObservationKinds: []string{ObservationLogError}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeLog}, RequireRuntime: true}},
		},
		ClaimPaymentStatusDistribution: {
			ClaimType:              ClaimPaymentStatusDistribution,
			Required:               []FieldRequirement{{Field: "payment_status_distribution", ObservationKinds: []string{ObservationStatusDistribution}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true}},
			RequiresCompleteResult: true,
		},
		ClaimCallbackTimeoutRelated: {
			ClaimType: ClaimCallbackTimeoutRelated,
			Required: []FieldRequirement{
				{Field: "payment_failure_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true},
				{Field: "callback_timeout_log", ObservationKinds: []string{ObservationLogError}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeLog}, RequireRuntime: true},
			},
		},
		ClaimCallbackTimeoutRootCause: {
			ClaimType: ClaimCallbackTimeoutRootCause,
			Required: []FieldRequirement{
				{Field: "payment_failure_anomaly", ObservationKinds: []string{ObservationMetricAnomaly}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true},
				{Field: "callback_timeout_log", ObservationKinds: []string{ObservationLogError}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeLog}, RequireRuntime: true},
				{Field: "recovery_or_exclusion_evidence", ObservationKinds: []string{ObservationStatusDistribution, ObservationMetricValue}, AllowedSourceKinds: []EvidenceSourceKind{SourceRuntimeMetric}, RequireRuntime: true},
			},
			AlternativeRequired: []FieldRequirement{
				{Field: "mechanism_or_runbook", ObservationKinds: []string{ObservationRunbookRule}},
				{
					Field:               "mechanism_or_static_code",
					ObservationKinds:    []string{ObservationStaticCodeDeclaration, ObservationStaticCodeCallSite},
					AllowedSourceKinds:  []EvidenceSourceKind{SourceStaticCode},
					RequireVersionMatch: true,
				},
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

func (r *ClaimRuleRegistry) Match(claim CandidateClaim, evidenceItems []EvidenceItem, failedQueries []FailedQuery) CoverageReport {
	report := r.matchEvidence(claim, evidenceItems, failedQueries)
	return applyAssertionDisposition(report)
}

func (r *ClaimRuleRegistry) matchEvidence(claim CandidateClaim, evidenceItems []EvidenceItem, failedQueries []FailedQuery) CoverageReport {
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

	referenced, missingEvidenceIDs := referencedEvidence(claim, evidenceItems)
	referencedFailedQueryIDs, missingFailedQueryIDs := referencedFailedQueries(claim, failedQueries)
	related := relatedEvidence(claim, referenced)
	report.RelatedEvidenceIDs = evidenceIDs(related)
	report.FailedQueryIDs = referencedFailedQueryIDs
	if len(missingEvidenceIDs) > 0 {
		report.MissingFields = append(report.MissingFields, FieldRequirement{Field: "referenced_evidence"})
	}
	if len(missingFailedQueryIDs) > 0 {
		report.MissingFields = append(report.MissingFields, FieldRequirement{Field: "referenced_failed_query"})
	}
	if len(claim.EvidenceIDs) == 0 && len(claim.FailedQueryIDs) == 0 {
		report.MissingFields = append(report.MissingFields, FieldRequirement{Field: "evidence_reference"})
	}
	if len(related) == 0 {
		if len(report.FailedQueryIDs) > 0 && len(missingEvidenceIDs) == 0 {
			report.Status = CoverageUnknown
		} else {
			report.Status = CoverageUncovered
		}
		if len(report.MissingFields) == 0 {
			report.MissingFields = append(report.MissingFields, rule.Required...)
		}
		return report
	}
	if expired := expiredEvidenceIDs(related); len(expired) > 0 {
		report.Status = CoverageStale
		report.MissingFields = append(report.MissingFields, FieldRequirement{Field: "fresh_runtime_evidence"})
		return report
	}
	claimFieldMatches, claimFieldMissing, claimFieldConflicts := matchClaimFields(claim, related)
	report.MatchedFields = append(report.MatchedFields, claimFieldMatches...)
	report.MissingFields = append(report.MissingFields, claimFieldMissing...)
	report.ConflictFields = append(report.ConflictFields, claimFieldConflicts...)
	for _, match := range claimFieldMatches {
		report.MatchedEvidenceIDs = appendUnique(report.MatchedEvidenceIDs, match.EvidenceID)
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
	if len(rule.AlternativeRequired) > 0 {
		match, ok := findRequirementMatchFromAlternatives(rule.AlternativeRequired, related)
		if ok {
			report.MatchedFields = append(report.MatchedFields, match)
			report.MatchedEvidenceIDs = appendUnique(report.MatchedEvidenceIDs, match.EvidenceID)
		} else {
			report.MissingFields = append(report.MissingFields, FieldRequirement{Field: "mechanism_or_runbook_or_aligned_code"})
		}
	}
	if len(missingEvidenceIDs) > 0 || len(missingFailedQueryIDs) > 0 {
		report.Status = CoveragePartial
		return report
	}

	report.ConflictFields = append(report.ConflictFields, findConflicts(rule.ConflictChecks, claim, related)...)
	if len(report.ConflictFields) > 0 {
		report.Status = CoverageConflict
		return report
	}
	if len(report.MissingFields) == 0 {
		if rule.RequiresCompleteResult && hasPartialEvidence(related) {
			report.Status = CoveragePartial
			return report
		}
		report.Status = CoverageCovered
		return report
	}
	report.Status = CoveragePartial
	return report
}

func hasPartialEvidence(items []EvidenceItem) bool {
	for _, item := range items {
		if item.ResultMetadata.Truncated || item.ResultMetadata.ReturnedRows < item.ResultMetadata.MaxRows && item.ResultMetadata.MaxRows > 0 {
			return true
		}
	}
	return false
}

func applyAssertionDisposition(report CoverageReport) CoverageReport {
	assertion := strings.TrimSpace(report.Claim.AssertionKind)
	if assertion == "" || assertion == AssertionFact {
		return report
	}

	switch assertion {
	case AssertionHypothesis:
		switch report.Status {
		case CoverageCovered:
			report.Status = CoverageSupported
		case CoveragePartial, CoverageUncovered, CoverageStale:
			report.Status = CoverageUnknown
		case CoverageConflict:
			report.Status = CoverageRefuted
		}
	case AssertionImplementation:
		if report.Status == CoverageCovered {
			report.Status = CoverageStaticallySupported
		} else if report.Status == CoveragePartial || report.Status == CoverageUncovered {
			report.Status = CoverageUnknown
		}
	case AssertionNextCheck:
		report.Status = CoverageActionOnly
	}
	return report
}

func matchClaimFields(claim CandidateClaim, evidenceItems []EvidenceItem) ([]FieldMatch, []FieldRequirement, []FieldConflict) {
	if len(claim.Fields) == 0 {
		return nil, nil, nil
	}

	keys := make([]string, 0, len(claim.Fields))
	for key := range claim.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var matches []FieldMatch
	var missing []FieldRequirement
	var conflicts []FieldConflict
	for _, key := range keys {
		expected := strings.TrimSpace(toString(claim.Fields[key]))
		if expected == "" {
			continue
		}
		found := false
		matched := false
		for _, item := range evidenceItems {
			for _, observation := range item.Observations {
				actual, present := observationFieldValue(item, observation, key)
				if !present {
					continue
				}
				found = true
				if strings.EqualFold(expected, actual) {
					matched = true
					matches = append(matches, FieldMatch{
						Field:            "claim_field:" + key,
						EvidenceID:       item.EvidenceID,
						ToolName:         item.ToolName,
						SourceKind:       item.SourceKind,
						Revision:         item.Revision,
						VersionAlignment: item.VersionAlignment,
						RuntimeVerified:  item.RuntimeVerified,
						Summary:          observation.Summary,
					})
					continue
				}
				conflicts = append(conflicts, FieldConflict{
					Field:      key,
					EvidenceID: item.EvidenceID,
					Expected:   expected,
					Actual:     actual,
				})
			}
		}
		if !found || !matched {
			missing = append(missing, FieldRequirement{Field: "claim_field:" + key, ClaimField: key})
		}
	}
	return matches, missing, conflicts
}

func observationFieldValue(_ EvidenceItem, observation Observation, key string) (string, bool) {
	if value, ok := observation.Fields[key]; ok {
		return strings.TrimSpace(toString(value)), true
	}
	if value, ok := observation.Subject.Fields[key]; ok {
		return strings.TrimSpace(value), true
	}
	if value, ok := observation.Scope.Fields[key]; ok {
		return strings.TrimSpace(value), true
	}
	return "", false
}

func referencedEvidence(claim CandidateClaim, evidenceItems []EvidenceItem) ([]EvidenceItem, []string) {
	if len(claim.EvidenceIDs) == 0 {
		return nil, nil
	}

	byID := make(map[string]EvidenceItem, len(evidenceItems))
	for _, item := range evidenceItems {
		if strings.TrimSpace(item.EvidenceID) != "" {
			byID[item.EvidenceID] = item
		}
	}

	var referenced []EvidenceItem
	var missing []string
	for _, evidenceID := range claim.EvidenceIDs {
		evidenceID = strings.TrimSpace(evidenceID)
		item, ok := byID[evidenceID]
		if !ok {
			missing = appendUnique(missing, evidenceID)
			continue
		}
		referenced = append(referenced, item)
	}
	return referenced, missing
}

func referencedFailedQueries(claim CandidateClaim, failedQueries []FailedQuery) ([]string, []string) {
	if len(claim.FailedQueryIDs) == 0 {
		return nil, nil
	}

	byID := make(map[string]bool, len(failedQueries))
	for _, query := range failedQueries {
		if strings.TrimSpace(query.FailedQueryID) != "" {
			byID[query.FailedQueryID] = true
		}
	}

	var referenced []string
	var missing []string
	for _, failedQueryID := range claim.FailedQueryIDs {
		failedQueryID = strings.TrimSpace(failedQueryID)
		if byID[failedQueryID] {
			referenced = appendUnique(referenced, failedQueryID)
			continue
		}
		missing = appendUnique(missing, failedQueryID)
	}
	return referenced, missing
}

func relatedEvidence(claim CandidateClaim, evidenceItems []EvidenceItem) []EvidenceItem {
	out := make([]EvidenceItem, 0, len(evidenceItems))
	for _, item := range evidenceItems {
		if strings.TrimSpace(item.EvidenceID) == "" || len(item.Observations) == 0 {
			continue
		}
		if isCrossSubjectPaymentClaim(claim.ClaimType) {
			if scopeRelated(claim.Scope, item) {
				out = append(out, item)
			}
			continue
		}
		if scopeRelated(claim.Scope, item) && (observationsRelated(claim.Subject, item.Observations) || claim.Subject.Service == "") {
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

func scopeRelated(claimScope EvidenceScope, item EvidenceItem) bool {
	for _, observation := range item.Observations {
		if scopeRefRelated(claimScope, observation.Scope) {
			return true
		}
	}
	return false
}

func scopeRefRelated(claimScope EvidenceScope, actualScope EvidenceScope) bool {
	if claimScope.TimeRange != "" {
		if actualScope.TimeRange != "" && !strings.EqualFold(claimScope.TimeRange, actualScope.TimeRange) {
			return false
		}
	}
	if claimScope.StartTime != "" {
		if actualScope.StartTime != "" && !strings.EqualFold(claimScope.StartTime, actualScope.StartTime) {
			return false
		}
	}
	if claimScope.EndTime != "" {
		if actualScope.EndTime != "" && !strings.EqualFold(claimScope.EndTime, actualScope.EndTime) {
			return false
		}
	}
	for key, expected := range claimScope.Fields {
		actual, ok := actualScope.Fields[key]
		if !ok || !strings.EqualFold(expected, actual) {
			return false
		}
	}
	return true
}

func findRequirementMatch(req FieldRequirement, evidenceItems []EvidenceItem) (FieldMatch, bool) {
	for _, item := range evidenceItems {
		for _, obs := range item.Observations {
			if kindAllowed(obs.Kind, req.ObservationKinds) && sourceAllowed(item, req) {
				return FieldMatch{
					Field:            req.Field,
					EvidenceID:       item.EvidenceID,
					ToolName:         item.ToolName,
					SourceKind:       item.SourceKind,
					Revision:         item.Revision,
					VersionAlignment: item.VersionAlignment,
					RuntimeVerified:  item.RuntimeVerified,
					Summary:          obs.Summary,
				}, true
			}
		}
	}
	return FieldMatch{}, false
}

func findRequirementMatchFromAlternatives(requirements []FieldRequirement, evidenceItems []EvidenceItem) (FieldMatch, bool) {
	for _, requirement := range requirements {
		if match, ok := findRequirementMatch(requirement, evidenceItems); ok {
			return match, true
		}
	}
	return FieldMatch{}, false
}

func sourceAllowed(item EvidenceItem, requirement FieldRequirement) bool {
	if (item.SourceKind == SourceStaticCode || item.SourceKind == SourceStaticConfig) && len(requirement.AllowedSourceKinds) == 0 {
		return false
	}
	if len(requirement.AllowedSourceKinds) > 0 {
		allowed := false
		for _, kind := range requirement.AllowedSourceKinds {
			if item.SourceKind == kind {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if requirement.RequireRuntime && !item.RuntimeVerified {
		return false
	}
	if requirement.RequireRuntime && item.ObservedAt.IsZero() {
		return false
	}
	if requirement.RequireVersionMatch && item.VersionAlignment != VersionAligned {
		return false
	}
	return true
}

func expiredEvidenceIDs(items []EvidenceItem) []string {
	now := time.Now().UTC()
	ids := make([]string, 0)
	for _, item := range items {
		if IsExpired(item, now) {
			ids = append(ids, item.EvidenceID)
		}
	}
	return ids
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
