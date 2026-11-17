package answer

import (
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
)

func matchClaimSet(claimSet evidence.ClaimSet, evidenceItems []evidence.EvidenceItem, failedQueries []evidence.FailedQuery) []evidence.CoverageReport {
	return matchClaimSetWithConflicts(claimSet, evidenceItems, failedQueries, nil)
}

func matchClaimSetWithConflicts(
	claimSet evidence.ClaimSet,
	evidenceItems []evidence.EvidenceItem,
	failedQueries []evidence.FailedQuery,
	conflicts []evidence.EvidenceConflict,
) []evidence.CoverageReport {
	registry := evidence.ClaimRuleRegistryWithPayment()
	reports := make([]evidence.CoverageReport, 0, len(claimSet.Claims))
	for _, claim := range claimSet.Claims {
		report := registry.Match(claim, evidenceItems, failedQueries)
		for _, conflict := range conflicts {
			if containsString(claim.EvidenceIDs, conflict.EvidenceID) {
				report.Status = evidence.CoverageConflict
				report.EvidenceConflicts = append(report.EvidenceConflicts, conflict)
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func evidenceFromToolEvidence(items []trace.ToolEvidence) ([]evidence.EvidenceItem, []evidence.FailedQuery) {
	evidenceItems, failedQueries, _ := evidenceFromToolEvidenceDetailed(items)
	return evidenceItems, failedQueries
}

func evidenceFromToolEvidenceDetailed(items []trace.ToolEvidence) ([]evidence.EvidenceItem, []evidence.FailedQuery, []evidence.EvidenceProtocolIssue) {
	var evidenceItems []evidence.EvidenceItem
	var failedQueries []evidence.FailedQuery
	var protocolIssues []evidence.EvidenceProtocolIssue
	for _, item := range items {
		env := item.Envelope
		if evidenceItem, ok := env.ToEvidenceItem(item.ToolName); ok {
			evidenceItems = append(evidenceItems, evidenceItem)
		}
		if env.Status == evidence.StatusProtocolError {
			protocolIssues = append(protocolIssues, evidence.EvidenceProtocolIssue{
				ToolName: item.ToolName,
				Reason:   env.Summary,
			})
		}
		if failedQuery, ok := env.ToFailedQuery(item.ToolName); ok {
			failedQueries = append(failedQueries, failedQuery)
		}
	}
	return evidenceItems, failedQueries, protocolIssues
}

func evidenceFromTrace(steps []trace.AgentStep) ([]evidence.EvidenceItem, []evidence.FailedQuery) {
	evidenceItems, failedQueries, _ := evidenceFromTraceDetailed(steps)
	return evidenceItems, failedQueries
}

func evidenceFromTraceDetailed(steps []trace.AgentStep) ([]evidence.EvidenceItem, []evidence.FailedQuery, []evidence.EvidenceProtocolIssue) {
	var evidenceItems []evidence.EvidenceItem
	var failedQueries []evidence.FailedQuery
	var protocolIssues []evidence.EvidenceProtocolIssue
	for _, step := range steps {
		if step.Phase != "end" && step.Phase != "error" {
			continue
		}
		for _, raw := range []string{step.Output, step.Error} {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			env := evidence.Parse(raw).Envelope
			if evidenceItem, ok := env.ToEvidenceItem(step.Name); ok {
				evidenceItems = append(evidenceItems, evidenceItem)
			}
			if env.Status == evidence.StatusProtocolError {
				protocolIssues = append(protocolIssues, evidence.EvidenceProtocolIssue{
					ToolName: step.Name,
					Reason:   env.Summary,
				})
			}
			if failedQuery, ok := env.ToFailedQuery(step.Name); ok {
				failedQueries = append(failedQueries, failedQuery)
			}
		}
	}
	return evidenceItems, failedQueries, protocolIssues
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func evidenceSetFromToolEvidence(items []trace.ToolEvidence) evidence.EvidenceSet {
	sets := make([]evidence.EvidenceSet, 0, len(items)*2)
	for _, item := range items {
		if item.EvidenceID() != "" {
			sets = append(sets, evidence.EvidenceSet{EvidenceIDs: map[string]bool{item.EvidenceID(): true}})
		}
		if item.FailedQueryID() != "" {
			sets = append(sets, evidence.EvidenceSet{FailedQueryIDs: map[string]bool{item.FailedQueryID(): true}})
		}
	}
	return evidence.Merge(sets...)
}

func evidenceSetFromReports(reports []evidence.CoverageReport) evidence.EvidenceSet {
	set := evidence.EvidenceSet{
		EvidenceIDs:    make(map[string]bool),
		FailedQueryIDs: make(map[string]bool),
	}
	for _, report := range reports {
		for _, evidenceID := range report.MatchedEvidenceIDs {
			set.EvidenceIDs[evidenceID] = true
		}
		for _, evidenceID := range report.RelatedEvidenceIDs {
			set.EvidenceIDs[evidenceID] = true
		}
		for _, failedQueryID := range report.FailedQueryIDs {
			set.FailedQueryIDs[failedQueryID] = true
		}
	}
	return set
}
