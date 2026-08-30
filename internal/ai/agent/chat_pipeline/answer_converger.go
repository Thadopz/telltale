package chat_pipeline

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"sort"
	"strings"
)

type AnswerConvergeInput struct {
	Question        string
	Answer          string
	Mode            ResponseMode
	DiagnosticState *DiagnosticState
	Steps           []trace.AgentStep
	ToolEvidence    []trace.ToolEvidence

	// ClaimExtractor and Localizer are injectable so deterministic tests do not
	// need a real model. Production callers may leave both fields nil.
	ClaimExtractor ClaimExtractor
	Localizer      AnswerLocalizer
	Recorder       *trace.Recorder
	PipelineResult *ClaimPipelineResult
	// AllowExtractionFallback is reserved for trusted system-generated
	// incidents. Interactive diagnosis keeps strict extraction failures.
	AllowExtractionFallback bool

	// Finalize is retained for source compatibility with older callers. It is
	// intentionally not used for evidence coverage anymore.
	Finalize func(context.Context, string, []trace.ToolEvidence) (string, error)
}

type ClaimPipelineResult struct {
	ClaimSet          evidence.ClaimSet
	CoverageReports   []evidence.CoverageReport
	Hypotheses        []Hypothesis
	EvidenceConflicts []evidence.EvidenceConflict
	ProtocolIssues    []evidence.EvidenceProtocolIssue
	ExtractionError   string
	LocalizationMode  string
}

type evidenceCollection struct {
	Items          []evidence.EvidenceItem
	FailedQueries  []evidence.FailedQuery
	Conflicts      []evidence.EvidenceConflict
	ProtocolIssues []evidence.EvidenceProtocolIssue
}

func ConvergeAnswer(ctx context.Context, input AnswerConvergeInput) (string, error) {
	draftAnswer := strings.TrimSpace(input.Answer)
	mode := normalizeResponseMode(input.Mode)
	collection := collectEvidence(input.Steps, input.ToolEvidence)
	evidenceItems, failedQueries := selectModelEvidence(collection.Items, collection.FailedQueries, input.DiagnosticState, collection.Conflicts)
	if input.PipelineResult != nil {
		input.PipelineResult.EvidenceConflicts = append([]evidence.EvidenceConflict(nil), collection.Conflicts...)
		input.PipelineResult.ProtocolIssues = append([]evidence.EvidenceProtocolIssue(nil), collection.ProtocolIssues...)
		if input.DiagnosticState != nil {
			input.PipelineResult.Hypotheses = append([]Hypothesis(nil), input.DiagnosticState.Hypotheses...)
		}
	}
	if mode == ResponseModeNormal {
		if draftAnswer == "" {
			return "", fmt.Errorf("normal chat answer is empty")
		}
		available := evidenceSetFromEvidenceItems(evidenceItems, failedQueries)
		if validation := evidence.ValidateAnswer(draftAnswer, available); !validation.OK {
			return "", fmt.Errorf("normal chat answer failed evidence validation: %s", strings.Join(validation.Reasons, "; "))
		}
		return draftAnswer, nil
	}
	if len(evidenceItems) == 0 && len(failedQueries) == 0 {
		if len(collection.ProtocolIssues) > 0 {
			return protocolIssueAnswer(collection.ProtocolIssues), nil
		}
		return noEvidenceAnswer(), nil
	}

	extractor := input.ClaimExtractor
	if extractor == nil {
		var err error
		extractor, err = defaultClaimExtractor(ctx)
		if err != nil {
			setClaimPipelineError(input.PipelineResult, err)
			recordClaimPipelineFailure(input.Recorder, err)
			return "", err
		}
	}

	claimSet, err := extractor.Extract(ctx, ClaimExtractionInput{
		Question:          strings.TrimSpace(input.Question),
		DraftAnswer:       draftAnswer,
		EvidenceItems:     evidenceItems,
		FailedQueries:     failedQueries,
		DiagnosticSummary: diagnosticSummary(input.Steps),
	})
	if err != nil {
		setClaimPipelineError(input.PipelineResult, err)
		recordClaimPipelineFailure(input.Recorder, err)
		if input.AllowExtractionFallback {
			return safeExtractionFallback(draftAnswer, evidenceItems, failedQueries, input.PipelineResult), nil
		}
		return "", err
	}
	claimSet = evidence.NormalizeClaimSet(claimSet)
	if err := evidence.ValidateClaimSet(claimSet); err != nil {
		setClaimPipelineError(input.PipelineResult, err)
		recordClaimPipelineFailure(input.Recorder, err)
		return "", err
	}

	reports := matchClaimSetWithConflicts(claimSet, evidenceItems, failedQueries, collection.Conflicts)
	if input.PipelineResult != nil {
		input.PipelineResult.ClaimSet = claimSet
		input.PipelineResult.CoverageReports = append([]evidence.CoverageReport(nil), reports...)
		input.PipelineResult.ExtractionError = ""
	}
	localizer := input.Localizer
	localizationMode := "template"
	if localizer == nil {
		localizer = NewTemplateAnswerLocalizer()
	} else {
		localizationMode = "custom"
	}
	answer, err := localizer.Localize(ctx, input.Question, reports)
	if err != nil {
		setClaimPipelineError(input.PipelineResult, err)
		recordClaimPipelineFailure(input.Recorder, err)
		return "", err
	}
	if strings.TrimSpace(answer) == "" {
		if input.Localizer != nil {
			answer, err = NewTemplateAnswerLocalizer().Localize(ctx, input.Question, reports)
			localizationMode = "template"
			if err != nil {
				setClaimPipelineError(input.PipelineResult, err)
				recordClaimPipelineFailure(input.Recorder, err)
				return "", err
			}
		}
		if strings.TrimSpace(answer) == "" {
			answer = noStructuredClaimAnswer()
		}
	}

	reportEvidence := evidenceSetFromReports(reports)
	if validation := evidence.ValidateAnswer(answer, reportEvidence); !validation.OK {
		recordClaimPipelineFailure(input.Recorder, fmt.Errorf("localized answer failed evidence validation: %s", strings.Join(validation.Reasons, "; ")))
		return "", fmt.Errorf("localized answer failed evidence validation: %s", strings.Join(validation.Reasons, "; "))
	}
	recordClaimPipeline(input.Recorder, claimSet, reports, localizationMode, collection.Conflicts)
	if input.PipelineResult != nil {
		input.PipelineResult.LocalizationMode = localizationMode
	}
	return strings.TrimSpace(answer), nil
}

func safeExtractionFallback(draftAnswer string, evidenceItems []evidence.EvidenceItem, failedQueries []evidence.FailedQuery, result *ClaimPipelineResult) string {
	if result != nil {
		result.LocalizationMode = "validated_draft_fallback"
	}
	available := evidenceSetFromEvidenceItems(evidenceItems, failedQueries)
	if draft := strings.TrimSpace(draftAnswer); draft != "" {
		if validation := evidence.ValidateAnswer(draft, available); validation.OK {
			return draft
		}
	}
	if result != nil {
		result.LocalizationMode = "extraction_gap"
	}
	return noStructuredClaimAnswer()
}

func setClaimPipelineError(result *ClaimPipelineResult, err error) {
	if result == nil || err == nil {
		return
	}
	result.ExtractionError = truncateClaimError(err)
}

func collectEvidence(steps []trace.AgentStep, toolEvidence []trace.ToolEvidence) evidenceCollection {
	fromTools, failedFromTools, toolIssues := evidenceFromToolEvidenceDetailed(toolEvidence)
	fromTrace, failedFromTrace, traceIssues := evidenceFromTraceDetailed(steps)
	evidenceItems, conflicts := mergeEvidenceItemsDetailed(fromTools, fromTrace)
	return evidenceCollection{
		Items:          evidenceItems,
		FailedQueries:  mergeFailedQueries(failedFromTools, failedFromTrace),
		Conflicts:      conflicts,
		ProtocolIssues: append(toolIssues, traceIssues...),
	}
}

func mergeEvidenceItems(groups ...[]evidence.EvidenceItem) []evidence.EvidenceItem {
	items, _ := mergeEvidenceItemsDetailed(groups...)
	return items
}

func mergeEvidenceItemsDetailed(groups ...[]evidence.EvidenceItem) ([]evidence.EvidenceItem, []evidence.EvidenceConflict) {
	byID := make(map[string]evidence.EvidenceItem)
	order := make([]string, 0)
	var conflicts []evidence.EvidenceConflict
	for _, group := range groups {
		for _, item := range group {
			id := strings.TrimSpace(item.EvidenceID)
			if id == "" {
				continue
			}
			previous, exists := byID[id]
			if !exists {
				order = append(order, id)
				byID[id] = item
				continue
			}
			firstDigest := evidence.DigestEvidenceItem(previous)
			secondDigest := evidence.DigestEvidenceItem(item)
			if firstDigest != secondDigest {
				conflicts = append(conflicts, evidence.EvidenceConflict{
					EvidenceID:     id,
					FirstDigest:    firstDigest,
					SecondDigest:   secondDigest,
					FirstToolName:  previous.ToolName,
					SecondToolName: item.ToolName,
				})
			}
		}
	}
	result := make([]evidence.EvidenceItem, 0, len(order))
	for _, id := range order {
		result = append(result, byID[id])
	}
	return result, conflicts
}

func mergeFailedQueries(groups ...[]evidence.FailedQuery) []evidence.FailedQuery {
	byID := make(map[string]evidence.FailedQuery)
	order := make([]string, 0)
	for _, group := range groups {
		for _, query := range group {
			id := strings.TrimSpace(query.FailedQueryID)
			if id == "" {
				continue
			}
			if _, exists := byID[id]; !exists {
				order = append(order, id)
			}
			byID[id] = query
		}
	}
	result := make([]evidence.FailedQuery, 0, len(order))
	for _, id := range order {
		result = append(result, byID[id])
	}
	return result
}

func evidenceSetFromEvidenceItems(items []evidence.EvidenceItem, failedQueries []evidence.FailedQuery) evidence.EvidenceSet {
	set := evidence.EvidenceSet{
		EvidenceIDs:    make(map[string]bool),
		FailedQueryIDs: make(map[string]bool),
	}
	for _, item := range items {
		if item.EvidenceID != "" {
			set.EvidenceIDs[item.EvidenceID] = true
		}
	}
	for _, query := range failedQueries {
		if query.FailedQueryID != "" {
			set.FailedQueryIDs[query.FailedQueryID] = true
		}
	}
	return set
}

func diagnosticSummary(steps []trace.AgentStep) string {
	var builder strings.Builder
	for _, step := range steps {
		if step.Phase != "end" && step.Phase != "error" {
			continue
		}
		fmt.Fprintf(&builder, "- phase=%s component=%s name=%s\n", step.Phase, step.Component, step.Name)
	}
	return strings.TrimSpace(builder.String())
}

func noEvidenceAnswer() string {
	return "Verified evidence:\n- No successful evidence_id is available.\n\nAssessment:\n- I cannot confirm the incident diagnosis without successful tool evidence.\n\nNext step:\n- Run targeted read-only diagnostics and capture structured evidence before making a factual diagnosis."
}

func noStructuredClaimAnswer() string {
	return "Evidence was collected, but no valid structured Claim was produced.\n\nAssessment:\n- No coverage result is available because evidence references must come from an explicit ClaimSet.\n\nNext step:\n- Produce a schema-valid ClaimSet with explicit evidence_id or failed_query_id references."
}

func protocolIssueAnswer(issues []evidence.EvidenceProtocolIssue) string {
	var builder strings.Builder
	builder.WriteString("Verified evidence:\n- No structured evidence is available.\n\n")
	builder.WriteString("Evidence protocol issues:\n")
	for _, issue := range issues {
		fmt.Fprintf(&builder, "- %s: %s\n", issue.ToolName, issue.Reason)
	}
	builder.WriteString("\nNext step:\n- Update the diagnostic tool result to return a structured evidence envelope.")
	return strings.TrimSpace(builder.String())
}

func recordClaimPipelineFailure(recorder *trace.Recorder, err error) {
	if recorder == nil {
		return
	}
	recorder.Record(trace.AgentStep{
		Agent:                 "chat_answer_converger",
		Phase:                 "error",
		Component:             "claim_extractor",
		Type:                  "structured_claim_extraction",
		Name:                  "claim_extractor",
		ClaimSchemaVersion:    evidence.ClaimSchemaVersion,
		ClaimExtractorSuccess: false,
		ClaimExtractorError:   truncateClaimError(err),
		LocalizationMode:      "template",
	})
}

func recordClaimPipeline(recorder *trace.Recorder, claimSet evidence.ClaimSet, reports []evidence.CoverageReport, localizationMode string, conflicts []evidence.EvidenceConflict) {
	if recorder == nil {
		return
	}
	statuses := make([]string, 0, len(reports))
	for _, report := range reports {
		statuses = append(statuses, report.Status)
	}
	sort.Strings(statuses)
	recorder.Record(trace.AgentStep{
		Agent:                 "chat_answer_converger",
		Phase:                 "end",
		Component:             "claim_extractor",
		Type:                  "structured_claim_extraction",
		Name:                  "claim_extractor",
		ClaimSchemaVersion:    claimSet.SchemaVersion,
		ClaimCount:            len(claimSet.Claims),
		ClaimExtractorSuccess: true,
		CoverageStatuses:      statuses,
		CoveredClaimCount:     countCoverage(reports, evidence.CoverageCovered),
		SupportedClaimCount:   countCoverage(reports, evidence.CoverageSupported),
		RefutedClaimCount:     countCoverage(reports, evidence.CoverageRefuted),
		PartialClaimCount:     countCoverage(reports, evidence.CoveragePartial),
		UnknownClaimCount:     countCoverage(reports, evidence.CoverageUnknown),
		UncoveredClaimCount:   countCoverage(reports, evidence.CoverageUncovered),
		ConflictClaimCount:    countCoverage(reports, evidence.CoverageConflict),
		EvidenceConflictCount: len(conflicts),
		LocalizationMode:      localizationMode,
	})
}

func countCoverage(reports []evidence.CoverageReport, status string) int {
	count := 0
	for _, report := range reports {
		if report.Status == status {
			count++
		}
	}
	return count
}

func truncateClaimError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) <= 1000 {
		return message
	}
	return message[:997] + "..."
}
