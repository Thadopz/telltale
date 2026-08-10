package evidence

import (
	"fmt"
	"reflect"
	"strings"
)

const ClaimSchemaVersion = 1

const (
	AssertionFact           = "fact"
	AssertionHypothesis     = "hypothesis"
	AssertionImplementation = "implementation_statement"
	AssertionNextCheck      = "next_check"
	maxClaimsPerSet         = 32
)

type FieldValueType string

const (
	FieldValueAny    FieldValueType = "any"
	FieldValueString FieldValueType = "string"
	FieldValueNumber FieldValueType = "number"
	FieldValueBool   FieldValueType = "bool"
)

type FieldSchema struct {
	Name     string         `json:"name"`
	Type     FieldValueType `json:"type"`
	Required bool           `json:"required,omitempty"`
}

type FreshnessPolicy string

const (
	FreshnessAny     FreshnessPolicy = "any"
	FreshnessCurrent FreshnessPolicy = "current_runtime"
)

type ClaimTypeSchema struct {
	ClaimType            string                 `json:"claim_type"`
	AllowedAssertions    []string               `json:"allowed_assertions"`
	AllowedFields        map[string]bool        `json:"allowed_fields,omitempty"`
	Fields               map[string]FieldSchema `json:"fields,omitempty"`
	RequiredFields       []FieldSchema          `json:"required_fields,omitempty"`
	AllowedSourceKinds   []EvidenceSourceKind   `json:"allowed_source_kinds,omitempty"`
	RequiresRuntime      bool                   `json:"requires_runtime,omitempty"`
	RequiresVersionMatch bool                   `json:"requires_version_match,omitempty"`
	FreshnessPolicy      FreshnessPolicy        `json:"freshness_policy,omitempty"`
}

var allowedClaimFields = map[string]bool{
	"actual_state":                   true,
	"baseline":                       true,
	"callee":                         true,
	"called_symbol":                  true,
	"component":                      true,
	"config_key":                     true,
	"config_value":                   true,
	"count":                          true,
	"current_value":                  true,
	"deployment_revision":            true,
	"duration_ms":                    true,
	"end_line":                       true,
	"endpoint":                       true,
	"error_code":                     true,
	"error_message":                  true,
	"external_service":               true,
	"environment":                    true,
	"failure_rate":                   true,
	"function":                       true,
	"image_digest":                   true,
	"impact_scope":                   true,
	"key":                            true,
	"line":                           true,
	"match_kind":                     true,
	"mechanism":                      true,
	"message":                        true,
	"method":                         true,
	"metric":                         true,
	"metric_anomaly":                 true,
	"metric_name":                    true,
	"metric_value":                   true,
	"orders_affected":                true,
	"path":                           true,
	"reason":                         true,
	"route":                          true,
	"rows_examined":                  true,
	"runtime_path":                   true,
	"service":                        true,
	"sql":                            true,
	"sql_digest":                     true,
	"start_line":                     true,
	"status":                         true,
	"status_distribution":            true,
	"symbol":                         true,
	"threshold":                      true,
	"traffic":                        true,
	"value":                          true,
	"window":                         true,
	"worker_state":                   true,
	"expected_state":                 true,
	"order_state":                    true,
	"rate":                           true,
	"log_error":                      true,
	"state_mismatch":                 true,
	"slow_sql":                       true,
	"runbook_rule":                   true,
	"implementation_contains":        true,
	"implementation_may_call":        true,
	"config_value_defined":           true,
	"error_mapped_at_code_location":  true,
	"payment_metric_anomaly":         true,
	"payment_order_state":            true,
	"payment_state_mismatch":         true,
	"payment_log_error":              true,
	"payment_status_distribution":    true,
	"payment_failure_anomaly":        true,
	"callback_timeout_log":           true,
	"recovery_or_exclusion_evidence": true,
	"mechanism_or_runbook":           true,
	"mechanism_or_static_code":       true,
}

var claimTypeFields = map[string][]string{
	ClaimMetricAnomaly:      {"metric", "metric_name", "metric_value", "metric_anomaly", "baseline", "threshold", "window", "service", "environment", "value", "current_value"},
	ClaimMetricValue:        {"metric", "metric_name", "metric_value", "baseline", "threshold", "window", "service", "environment", "value", "current_value"},
	ClaimLogError:           {"error_code", "error_message", "message", "route", "endpoint", "component", "service", "environment", "log_error"},
	ClaimStatusDistribution: {"status_distribution", "status", "count", "rate", "window", "service", "environment"},
	ClaimStateMismatch:      {"actual_state", "expected_state", "current_value", "baseline", "status", "order_state", "service", "environment"},
	ClaimSlowSQL:            {"sql", "sql_digest", "duration_ms", "rows_examined", "threshold", "service", "environment"},
	ClaimRunbookRule:        {"reason", "mechanism", "message", "runbook_rule", "service", "environment"},

	ClaimPaymentMetricAbnormal:     {"metric", "metric_name", "metric_value", "metric_anomaly", "payment_metric_anomaly", "baseline", "threshold", "window", "service", "environment", "value", "current_value"},
	ClaimPaymentOrderState:         {"order_state", "status", "order_state", "service", "environment"},
	ClaimPaymentStateMismatch:      {"actual_state", "expected_state", "payment_state_mismatch", "status", "service", "environment"},
	ClaimPaymentLogError:           {"error_code", "error_message", "message", "route", "endpoint", "component", "worker_state", "traffic", "payment_log_error", "service", "environment"},
	ClaimPaymentStatusDistribution: {"status_distribution", "payment_status_distribution", "status", "count", "rate", "window", "service", "environment"},
	ClaimCallbackTimeoutRelated:    {"error_code", "route", "component", "worker_state", "traffic", "payment_failure_anomaly", "callback_timeout_log", "recovery_or_exclusion_evidence", "service", "environment"},
	ClaimCallbackTimeoutRootCause:  {"error_code", "route", "component", "worker_state", "traffic", "payment_failure_anomaly", "callback_timeout_log", "recovery_or_exclusion_evidence", "mechanism_or_runbook", "mechanism_or_static_code", "service", "environment"},

	ClaimImplementationContains:    {"path", "symbol", "function", "line", "start_line", "end_line", "match_kind", "config_key", "config_value", "implementation_contains", "service", "environment"},
	ClaimImplementationMayCall:     {"path", "symbol", "function", "callee", "called_symbol", "route", "line", "start_line", "end_line", "match_kind", "implementation_may_call", "service", "environment"},
	ClaimConfigValueDefined:        {"path", "config_key", "config_value", "key", "value", "line", "start_line", "end_line", "match_kind", "config_value_defined", "service", "environment"},
	ClaimErrorMappedAtCodeLocation: {"path", "symbol", "function", "error_code", "error_message", "line", "start_line", "end_line", "match_kind", "error_mapped_at_code_location", "service", "environment"},
}

// ClaimSet is the language-independent output of the claim extraction step.
// Coverage status is deliberately not part of this structure. Matcher owns
// that decision after checking the referenced evidence.
type ClaimSet struct {
	SchemaVersion int              `json:"schema_version"`
	Claims        []CandidateClaim `json:"claims"`
}

// NormalizeClaimSet removes empty identifiers and duplicate evidence
// references without changing the meaning of a claim.
func NormalizeClaimSet(input ClaimSet) ClaimSet {
	result := ClaimSet{
		SchemaVersion: input.SchemaVersion,
		Claims:        make([]CandidateClaim, 0, len(input.Claims)),
	}
	for _, inputClaim := range input.Claims {
		claim := inputClaim
		claim.ClaimID = strings.TrimSpace(claim.ClaimID)
		claim.ClaimType = strings.TrimSpace(claim.ClaimType)
		claim.AssertionKind = strings.TrimSpace(claim.AssertionKind)
		claim.Predicate = strings.TrimSpace(claim.Predicate)
		claim.EvidenceIDs = uniqueNonEmptyStrings(claim.EvidenceIDs)
		claim.FailedQueryIDs = uniqueNonEmptyStrings(claim.FailedQueryIDs)
		result.Claims = append(result.Claims, claim)
	}
	return result
}

func ValidateClaimSet(input ClaimSet) error {
	claimSet := NormalizeClaimSet(input)
	if claimSet.SchemaVersion != ClaimSchemaVersion {
		return fmt.Errorf("unsupported claim schema version %d", claimSet.SchemaVersion)
	}
	if len(claimSet.Claims) > maxClaimsPerSet {
		return fmt.Errorf("claim set contains too many claims: %d", len(claimSet.Claims))
	}

	seenIDs := make(map[string]bool, len(claimSet.Claims))
	for index, claim := range claimSet.Claims {
		if claim.ClaimID == "" {
			return fmt.Errorf("claim %d is missing claim_id", index)
		}
		if seenIDs[claim.ClaimID] {
			return fmt.Errorf("duplicate claim_id %q", claim.ClaimID)
		}
		seenIDs[claim.ClaimID] = true
		if !ValidClaimType(claim.ClaimType) {
			return fmt.Errorf("claim %q has unsupported claim_type %q", claim.ClaimID, claim.ClaimType)
		}
		if !validAssertionKind(claim.AssertionKind) {
			return fmt.Errorf("claim %q has unsupported assertion_kind %q", claim.ClaimID, claim.AssertionKind)
		}
		claimSchema := ClaimTypeSchemaFor(claim.ClaimType)
		if !claimAssertionAllowed(claim.AssertionKind, claimSchema.AllowedAssertions) {
			return fmt.Errorf("claim %q assertion_kind %q is not allowed for claim_type %q", claim.ClaimID, claim.AssertionKind, claim.ClaimType)
		}
		for field := range claim.Fields {
			if !ValidClaimField(field) {
				return fmt.Errorf("claim %q has unsupported field %q", claim.ClaimID, field)
			}
			if len(claimSchema.AllowedFields) > 0 && !claimSchema.AllowedFields[field] {
				return fmt.Errorf("claim %q field %q is not allowed for claim_type %q", claim.ClaimID, field, claim.ClaimType)
			}
			fieldSchema, ok := claimSchema.Fields[field]
			if ok && !validClaimFieldValue(claim.Fields[field], fieldSchema.Type) {
				return fmt.Errorf("claim %q field %q has invalid type", claim.ClaimID, field)
			}
		}
		for field, fieldSchema := range claimSchema.Fields {
			if fieldSchema.Required {
				if _, ok := claim.Fields[field]; !ok {
					return fmt.Errorf("claim %q is missing required field %q", claim.ClaimID, field)
				}
			}
		}
	}
	return nil
}

func ClaimTypeSchemaFor(claimType string) ClaimTypeSchema {
	claimType = strings.TrimSpace(claimType)
	allowed := make(map[string]bool)
	for _, field := range claimTypeFields[claimType] {
		if allowedClaimFields[field] {
			allowed[field] = true
		}
	}
	// A registered claim type should have an explicit field schema. Keeping a
	// narrow fallback makes this function safe for callers inspecting an
	// unknown type without silently turning the global field list back on.
	if len(allowed) == 0 {
		allowed["value"] = true
	}
	allFields := typedClaimFields()
	fields := make(map[string]FieldSchema, len(allowed))
	for field := range allowed {
		fields[field] = allFields[field]
	}
	schema := ClaimTypeSchema{
		ClaimType:         claimType,
		AllowedAssertions: allowedAssertionsForClaimType(claimType),
		AllowedFields:     allowed,
		Fields:            fields,
		FreshnessPolicy:   FreshnessAny,
	}
	if rule, ok := ClaimRulesWithPayment()[claimType]; ok {
		for _, requirement := range append(append([]FieldRequirement(nil), rule.Required...), rule.AlternativeRequired...) {
			for _, sourceKind := range requirement.AllowedSourceKinds {
				schema.AllowedSourceKinds = appendUniqueSourceKind(schema.AllowedSourceKinds, sourceKind)
			}
			if requirement.RequireRuntime {
				schema.RequiresRuntime = true
				schema.FreshnessPolicy = FreshnessCurrent
			}
			if requirement.RequireVersionMatch {
				schema.RequiresVersionMatch = true
			}
		}
	}
	return schema
}

func allowedAssertionsForClaimType(claimType string) []string {
	switch strings.TrimSpace(claimType) {
	case ClaimImplementationContains, ClaimImplementationMayCall, ClaimConfigValueDefined, ClaimErrorMappedAtCodeLocation:
		return []string{AssertionImplementation, AssertionNextCheck}
	case ClaimCallbackTimeoutRootCause:
		return []string{AssertionHypothesis, AssertionNextCheck}
	case ClaimCallbackTimeoutRelated:
		return []string{AssertionFact, AssertionHypothesis, AssertionNextCheck}
	default:
		return []string{AssertionFact, AssertionHypothesis, AssertionNextCheck}
	}
}

func appendUniqueSourceKind(values []EvidenceSourceKind, value EvidenceSourceKind) []EvidenceSourceKind {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func claimAssertionAllowed(assertion string, allowed []string) bool {
	for _, candidate := range allowed {
		if assertion == candidate {
			return true
		}
	}
	return false
}

func validClaimFieldValue(value any, valueType FieldValueType) bool {
	if valueType == FieldValueAny || value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	switch valueType {
	case FieldValueString:
		return kind == reflect.String
	case FieldValueBool:
		return kind == reflect.Bool
	case FieldValueNumber:
		return kind >= reflect.Int && kind <= reflect.Float64
	default:
		return false
	}
}

func typedClaimFields() map[string]FieldSchema {
	fields := make(map[string]FieldSchema)
	for name := range allowedClaimFields {
		fields[name] = FieldSchema{Name: name, Type: FieldValueString}
	}
	for _, name := range []string{
		"count", "duration_ms", "failure_rate", "orders_affected", "rows_examined", "start_line", "end_line", "threshold", "value", "rate", "current_value", "baseline",
	} {
		fields[name] = FieldSchema{Name: name, Type: FieldValueNumber}
	}
	return fields
}

func ValidClaimField(field string) bool {
	return allowedClaimFields[strings.TrimSpace(field)]
}

func validAssertionKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case AssertionFact, AssertionHypothesis, AssertionImplementation, AssertionNextCheck:
		return true
	default:
		return false
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
