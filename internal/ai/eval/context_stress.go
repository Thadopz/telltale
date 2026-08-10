package eval

import (
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"strings"
)

const (
	contextStressNormalVariant = "normal"
	contextStressStressVariant = "stress"
)

var contextStressDefaultFacts = []string{
	"ERR_PAYMENT_CALLBACK_TIMEOUT",
	"/api/v1/payment/callback",
	"ev_payment_callback_history",
	"callback worker disabled",
	"external traffic normal",
}

var contextStressForbiddenMarkers = []string{
	"noise",
	"dummy",
	"mock",
	"fake",
	"test",
	"irrelevant",
	"sample",
	"distraction",
}

type contextStressToolGroup struct {
	Name        string
	DisplayName string
}

type contextStressToolOperation struct {
	Verb        string
	Noun        string
	Description string
}

var contextStressToolGroups = []contextStressToolGroup{
	{Name: "inventory", DisplayName: "inventory operations"},
	{Name: "warehouse", DisplayName: "warehouse operations"},
	{Name: "customer_sync", DisplayName: "customer synchronization"},
	{Name: "search_cluster", DisplayName: "search cluster"},
	{Name: "audit", DisplayName: "audit events"},
	{Name: "email_delivery", DisplayName: "email delivery"},
	{Name: "feature_flag", DisplayName: "feature flag evaluation"},
	{Name: "tax_calculation", DisplayName: "tax calculation"},
	{Name: "report_generation", DisplayName: "report generation"},
	{Name: "session_store", DisplayName: "session storage"},
	{Name: "scheduler", DisplayName: "scheduled jobs"},
	{Name: "document_pipeline", DisplayName: "document processing"},
	{Name: "recommendation", DisplayName: "recommendation serving"},
	{Name: "shipping", DisplayName: "shipping operations"},
	{Name: "product_catalog", DisplayName: "product catalog"},
	{Name: "fraud_review", DisplayName: "fraud review"},
	{Name: "analytics", DisplayName: "analytics ingestion"},
	{Name: "entitlement", DisplayName: "entitlement checks"},
	{Name: "media_processing", DisplayName: "media processing"},
	{Name: "notification_preferences", DisplayName: "notification preferences"},
}

var contextStressToolOperations = []contextStressToolOperation{
	{Verb: "get", Noun: "health", Description: "Retrieve the current health summary for"},
	{Verb: "query", Noun: "status", Description: "Query the current status distribution for"},
	{Verb: "inspect", Noun: "jobs", Description: "Inspect recent job execution details for"},
	{Verb: "list", Noun: "events", Description: "List recent operational events for"},
	{Verb: "get", Noun: "latency", Description: "Retrieve recent latency measurements for"},
}

func contextStressVariants() []string {
	return []string{contextStressNormalVariant, contextStressStressVariant}
}

func normalizeContextStressSpec(input *ContextStressSpec) ContextStressSpec {
	spec := ContextStressSpec{}
	if input != nil {
		spec = *input
	}
	if len(spec.CriticalFacts) == 0 {
		spec.CriticalFacts = append([]string(nil), contextStressDefaultFacts...)
	}
	if spec.HistoryNoisePairs <= 0 {
		spec.HistoryNoisePairs = 30
	}
	if spec.SummaryNoiseChars <= 0 {
		spec.SummaryNoiseChars = 36000
	}
	if spec.ToolResultNoiseChars <= 0 {
		spec.ToolResultNoiseChars = 36000
	}
	if spec.DistractorToolCount <= 0 {
		spec.DistractorToolCount = 100
	}
	spec.NormalProfile = normalizeContextBudgetSpec(spec.NormalProfile, contextStressNormalVariant)
	spec.StressProfile = normalizeContextBudgetSpec(spec.StressProfile, contextStressStressVariant)
	return spec
}

func normalizeContextBudgetSpec(input ContextBudgetSpec, variant string) ContextBudgetSpec {
	spec := input
	if strings.TrimSpace(spec.ModelName) == "" {
		spec.ModelName = "context-stress-" + variant
	}
	if spec.ContextWindowTokens <= 0 {
		if variant == contextStressStressVariant {
			spec.ContextWindowTokens = 32768
		} else {
			spec.ContextWindowTokens = 1048576
		}
	}
	if spec.MaxOutputTokens <= 0 {
		spec.MaxOutputTokens = 8192
	}
	if spec.WorkingPromptCapTokens <= 0 {
		if variant == contextStressStressVariant {
			spec.WorkingPromptCapTokens = 32768
		} else {
			spec.WorkingPromptCapTokens = 131072
		}
	}
	if spec.SafetyMarginRatio <= 0 {
		spec.SafetyMarginRatio = 0.10
	}
	if spec.MinSafetyMarginTokens <= 0 {
		spec.MinSafetyMarginTokens = 2048
	}
	return spec
}

func contextStressProfile(spec ContextStressSpec, variant string) contextbudget.ModelContextProfile {
	budgetSpec := spec.NormalProfile
	if variant == contextStressStressVariant {
		budgetSpec = spec.StressProfile
	}
	return contextbudget.ModelContextProfile{
		ConfigKey:             "eval.context_stress." + variant,
		ModelName:             budgetSpec.ModelName,
		ContextWindowTokens:   budgetSpec.ContextWindowTokens,
		MaxOutputTokens:       budgetSpec.MaxOutputTokens,
		WorkingPromptCap:      budgetSpec.WorkingPromptCapTokens,
		SafetyMarginRatio:     budgetSpec.SafetyMarginRatio,
		MinSafetyMarginTokens: budgetSpec.MinSafetyMarginTokens,
	}
}

func prepareContextStressCase(input Case, variant string) (Case, error) {
	spec := normalizeContextStressSpec(input.ContextStress)
	if variant != contextStressNormalVariant && variant != contextStressStressVariant {
		return Case{}, fmt.Errorf("unsupported context stress variant %q", variant)
	}
	if spec.DistractorToolCount > len(contextStressToolGroups)*len(contextStressToolOperations) {
		return Case{}, fmt.Errorf("context stress requires at most %d distractor tools", len(contextStressToolGroups)*len(contextStressToolOperations))
	}

	prepared := input
	prepared.ContextStress = &spec
	prepared.History = contextStressHistory(input.History, spec.HistoryNoisePairs)
	prepared.Context.Summary = contextStressLayer(input.Context.Summary, spec.SummaryNoiseChars, "summary", spec.CriticalFacts)
	prepared.Context.ToolResults = contextStressToolResults(input.Context.ToolResults, spec.ToolResultNoiseChars, spec.CriticalFacts)
	prepared.ToolFixtures = contextStressToolFixtures(spec.DistractorToolCount)
	return prepared, nil
}

func contextStressHistory(existing []ChatMessage, pairs int) []ChatMessage {
	history := make([]ChatMessage, 0, len(existing)+pairs*2)
	history = append(history, existing...)
	for index := 1; index <= pairs; index++ {
		history = append(history,
			ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("Review the routine warehouse reconciliation batch %03d and record its completion time.", index),
			},
			ChatMessage{
				Role:    "assistant",
				Content: fmt.Sprintf("Batch %03d was recorded for the normal operating window.", index),
			},
		)
	}
	return history
}

func contextStressLayer(existing string, targetChars int, name string, facts []string) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(existing))
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	for index := 1; builder.Len() < targetChars; index++ {
		fmt.Fprintf(&builder, "Earlier %s record %04d: routine operational review completed for the requested scope. ", name, index)
	}
	builder.WriteString("\n\nVerified runtime facts:\n")
	for _, fact := range facts {
		builder.WriteString("- ")
		builder.WriteString(fact)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func contextStressToolResults(existing string, targetChars int, facts []string) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(existing))
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	for index := 1; builder.Len() < targetChars; index++ {
		fmt.Fprintf(&builder, "Operational observation %04d: the requested background component remained within its normal range. ", index)
	}
	builder.WriteString("\n\nPayment runtime evidence:\n")
	for _, fact := range facts {
		builder.WriteString("- ")
		builder.WriteString(fact)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func contextStressToolFixtures(distractorCount int) []ToolFixture {
	fixtures := make([]ToolFixture, 0, distractorCount+1)
	fixtures = append(fixtures, ToolFixture{
		Name:        "payment_agent",
		Description: "Inspect payment and order runtime evidence for callback failures, impact scope, and associated evidence identifiers.",
		Parameters: map[string]ToolParameterFixture{
			"query": {
				Type:        "string",
				Description: "The payment incident question and relevant identifiers.",
				Required:    true,
			},
			"time_range": {
				Type:        "string",
				Description: "The ISO-8601 time range to inspect.",
			},
		},
		Output: `{"success":true,"status":"success","evidence_id":"ev_payment_callback_history","can_use_as_evidence":true,"data_status":"present","source_kind":"runtime_log","runtime_verified":true,"subject":{"type":"payment_callback","service":"payment"},"observations":[{"kind":"log_error","subject":{"type":"payment_callback","service":"payment"},"fields":{"error_code":"ERR_PAYMENT_CALLBACK_TIMEOUT","route":"/api/v1/payment/callback","component":"callback worker","worker_state":"disabled","traffic":"external traffic normal"},"summary":"Payment callback timeout was observed while the callback worker was disabled."}],"message":"ERR_PAYMENT_CALLBACK_TIMEOUT observed on /api/v1/payment/callback; callback worker disabled; external traffic normal","summary":"Payment callback failures are associated with a disabled callback worker while external traffic remains normal."}`,
	})

	for _, group := range contextStressToolGroups {
		for _, operation := range contextStressToolOperations {
			if len(fixtures)-1 >= distractorCount {
				return fixtures
			}
			name := operation.Verb + "_" + group.Name + "_" + operation.Noun
			fixtures = append(fixtures, ToolFixture{
				Name: name,
				Description: fmt.Sprintf(
					"%s %s. Use it to correlate the requested environment and time window with operational records.",
					operation.Description,
					group.DisplayName,
				),
				Parameters: map[string]ToolParameterFixture{
					"environment": {
						Type:        "string",
						Description: "Deployment environment or region to inspect.",
						Required:    true,
					},
					"time_range": {
						Type:        "string",
						Description: "The ISO-8601 time window for the query.",
						Required:    true,
					},
					"resource_id": {
						Type:        "string",
						Description: "Optional identifier for the selected operational resource.",
					},
				},
				Output: `{"success":true,"status":"success","message":"No anomaly detected for the requested scope.","scope":"production"}`,
			})
		}
	}
	return fixtures
}

func contextStressToolMarkerHits(fixtures []ToolFixture) []string {
	var hits []string
	for _, fixture := range fixtures {
		values := []string{fixture.Name, fixture.Description, fixture.Output, fixture.Error}
		for name, parameter := range fixture.Parameters {
			values = append(values, name, parameter.Description)
		}
		for _, value := range values {
			lowerValue := strings.ToLower(value)
			for _, marker := range contextStressForbiddenMarkers {
				if strings.Contains(lowerValue, marker) {
					hits = appendUniqueStrings(hits, fixture.Name+":"+marker)
				}
			}
		}
	}
	return hits
}

func contextStressDistractorNames(fixtures []ToolFixture) map[string]bool {
	names := make(map[string]bool)
	for _, fixture := range fixtures {
		if fixture.Name != "payment_agent" {
			names[fixture.Name] = true
		}
	}
	return names
}
