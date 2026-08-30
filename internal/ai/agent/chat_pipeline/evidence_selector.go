package chat_pipeline

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

const (
	maxSelectedEvidence      = 16
	maxEvidencePerHypothesis = 3
	maxModelEvidenceBytes    = 64 * 1024
	maxFailedQueriesForModel = 16
)

type rankedEvidence struct {
	item       evidence.EvidenceItem
	score      int
	hypotheses []string
}

// selectModelEvidence keeps model input bounded independently of the queried
// time span. It favors runtime, complete, scoped evidence and preserves a
// compact form when a full item would exceed the remaining byte budget.
func selectModelEvidence(items []evidence.EvidenceItem, failed []evidence.FailedQuery, state *DiagnosticState, conflicts []evidence.EvidenceConflict) ([]evidence.EvidenceItem, []evidence.FailedQuery) {
	selectedFailed := selectFailedQueries(failed)
	usedBytes := encodedSize(selectedFailed)
	conflicted := make(map[string]bool, len(conflicts))
	for _, conflict := range conflicts {
		conflicted[conflict.EvidenceID] = true
	}

	now := time.Now().UTC()
	ranked := make([]rankedEvidence, 0, len(items))
	for _, item := range items {
		if item.EvidenceID == "" || len(item.Observations) == 0 {
			continue
		}
		if item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
			continue
		}
		if hasInvalidObservationScope(item) {
			continue
		}
		if !evidenceMatchesExpectedScope(item, state) {
			continue
		}
		candidate := rankedEvidence{item: item, score: evidenceSelectionScore(item, state, conflicted[item.EvidenceID])}
		if state != nil {
			for _, hypothesis := range state.Hypotheses {
				if evidenceItemSupportsHypothesis(item, hypothesis) {
					candidate.hypotheses = append(candidate.hypotheses, hypothesis.ID)
				}
			}
		}
		if len(candidate.hypotheses) == 0 {
			if state == nil {
				candidate.hypotheses = []string{"unassigned:" + item.EvidenceID}
			} else {
				candidate.hypotheses = []string{"other"}
			}
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score > ranked[right].score
		}
		if !ranked[left].item.ObservedAt.Equal(ranked[right].item.ObservedAt) {
			return ranked[left].item.ObservedAt.After(ranked[right].item.ObservedAt)
		}
		return ranked[left].item.EvidenceID < ranked[right].item.EvidenceID
	})

	perHypothesis := make(map[string]int)
	selected := make([]evidence.EvidenceItem, 0, minInt(len(ranked), maxSelectedEvidence))
	for _, candidate := range ranked {
		if len(selected) >= maxSelectedEvidence || usedBytes >= maxModelEvidenceBytes {
			break
		}
		if hypothesisBudgetFull(candidate.hypotheses, perHypothesis) {
			continue
		}
		item := candidate.item
		itemBytes := encodedSize(item)
		if usedBytes+itemBytes > maxModelEvidenceBytes {
			item = compactEvidenceItem(item)
			itemBytes = encodedSize(item)
		}
		if usedBytes+itemBytes > maxModelEvidenceBytes {
			continue
		}
		selected = append(selected, item)
		usedBytes += itemBytes
		for _, hypothesisID := range candidate.hypotheses {
			perHypothesis[hypothesisID]++
		}
	}
	return selected, selectedFailed
}

func evidenceSelectionScore(item evidence.EvidenceItem, state *DiagnosticState, conflicted bool) int {
	score := 0
	if item.RuntimeVerified {
		score += 100
	}
	switch item.SourceKind {
	case evidence.SourceRuntimeMetric, evidence.SourceRuntimeLog, evidence.SourceRuntimeDB:
		score += 50
	}
	if !item.ResultMetadata.Truncated {
		score += 20
	}
	if hasScopedObservation(item) {
		score += 10
	}
	if hasSubjectObservation(item) {
		score += 10
	}
	if state != nil {
		for _, observation := range item.Observations {
			if observation.Scope.StartTime == state.ExpectedStartTime && observation.Scope.EndTime == state.ExpectedEndTime {
				score += 30
				break
			}
		}
		for _, observation := range item.Observations {
			if state.ExpectedDatabaseTarget != "" && observation.Scope.Fields["target"] == state.ExpectedDatabaseTarget {
				score += 30
				break
			}
		}
	}
	if conflicted {
		score -= 200
	}
	return score
}

func evidenceMatchesExpectedScope(item evidence.EvidenceItem, state *DiagnosticState) bool {
	if state == nil || state.ExpectedStartTime == "" || state.ExpectedEndTime == "" {
		return true
	}
	expectedStart, startErr := time.Parse(time.RFC3339, state.ExpectedStartTime)
	expectedEnd, endErr := time.Parse(time.RFC3339, state.ExpectedEndTime)
	if startErr != nil || endErr != nil || !expectedStart.Before(expectedEnd) {
		return false
	}
	for _, observation := range item.Observations {
		itemStartText := strings.TrimSpace(observation.Scope.StartTime)
		itemEndText := strings.TrimSpace(observation.Scope.EndTime)
		if itemStartText == "" && itemEndText == "" {
			continue
		}
		itemStart, startErr := time.Parse(time.RFC3339, itemStartText)
		itemEnd, endErr := time.Parse(time.RFC3339, itemEndText)
		if startErr != nil || endErr != nil || !itemStart.Before(itemEnd) {
			continue
		}
		if observation.Kind == evidence.ObservationSlowSQL {
			if itemStart.Equal(expectedStart) && itemEnd.Equal(expectedEnd) &&
				state.ExpectedDatabaseTarget != "" && observation.Scope.Fields["target"] == state.ExpectedDatabaseTarget {
				return true
			}
			continue
		}
		if itemEnd.After(expectedStart) && itemStart.Before(expectedEnd) {
			return true
		}
	}
	return !hasSlowSQLObservation(item) && !hasScopedObservation(item)
}

func hasSlowSQLObservation(item evidence.EvidenceItem) bool {
	for _, observation := range item.Observations {
		if observation.Kind == evidence.ObservationSlowSQL {
			return true
		}
	}
	return false
}

func evidenceItemSupportsHypothesis(item evidence.EvidenceItem, hypothesis Hypothesis) bool {
	for _, observation := range item.Observations {
		switch observation.Kind {
		case evidence.ObservationSlowSQL:
			if hypothesis.ID == "dependency_or_database" || hypothesis.ID == "dependency_or_log" {
				return true
			}
		case evidence.ObservationLogError:
			if hypothesis.ID == "runtime_signal" || hypothesis.ID == "dependency_or_database" || hypothesis.ID == "dependency_or_log" {
				return true
			}
		case evidence.ObservationMetricAnomaly, evidence.ObservationMetricValue, evidence.ObservationStatusDistribution:
			if hypothesis.ID == "runtime_signal" || hypothesis.ID == "traffic_or_resource_pressure" {
				return true
			}
		case evidence.ObservationStaticCodeDeclaration, evidence.ObservationStaticCodeCallSite, evidence.ObservationStaticConfig, evidence.ObservationStaticRoute, evidence.ObservationStaticSQL:
			if hypothesis.ID == "change_or_code_path" {
				return true
			}
		}
	}
	return false
}

func invalidEvidenceTimeScope(scope evidence.EvidenceScope) bool {
	if strings.TrimSpace(scope.StartTime) == "" && strings.TrimSpace(scope.EndTime) == "" {
		return false
	}
	if strings.TrimSpace(scope.StartTime) == "" || strings.TrimSpace(scope.EndTime) == "" {
		return true
	}
	start, startErr := time.Parse(time.RFC3339, scope.StartTime)
	end, endErr := time.Parse(time.RFC3339, scope.EndTime)
	return startErr != nil || endErr != nil || !start.Before(end)
}

func hasInvalidObservationScope(item evidence.EvidenceItem) bool {
	for _, observation := range item.Observations {
		if invalidEvidenceTimeScope(observation.Scope) {
			return true
		}
	}
	return false
}

func hasScopedObservation(item evidence.EvidenceItem) bool {
	for _, observation := range item.Observations {
		scope := observation.Scope
		if scope.StartTime != "" || scope.EndTime != "" || scope.TimeRange != "" || len(scope.Fields) > 0 {
			return true
		}
	}
	return false
}

func hasSubjectObservation(item evidence.EvidenceItem) bool {
	for _, observation := range item.Observations {
		subject := observation.Subject
		if subject.Service != "" || subject.OrderNo != "" || subject.TraceID != "" || len(subject.Fields) > 0 {
			return true
		}
	}
	return false
}

func hypothesisBudgetFull(hypotheses []string, counts map[string]int) bool {
	for _, hypothesisID := range hypotheses {
		if counts[hypothesisID] >= maxEvidencePerHypothesis {
			return true
		}
	}
	return false
}

func compactEvidenceItem(item evidence.EvidenceItem) evidence.EvidenceItem {
	compact := item
	compact.Summary = truncateEvidenceText(item.Summary, 512) + " [details omitted by evidence budget]"
	compact.Observations = make([]evidence.Observation, 0, len(item.Observations))
	for _, observation := range item.Observations {
		compact.Observations = append(compact.Observations, evidence.Observation{
			Kind:    observation.Kind,
			Subject: observation.Subject,
			Scope:   observation.Scope,
			Summary: truncateEvidenceText(observation.Summary, 256),
		})
	}
	return compact
}

func selectFailedQueries(items []evidence.FailedQuery) []evidence.FailedQuery {
	if len(items) > maxFailedQueriesForModel {
		items = items[:maxFailedQueriesForModel]
	}
	selected := make([]evidence.FailedQuery, 0, len(items))
	for _, item := range items {
		item.Summary = truncateEvidenceText(item.Summary, 300)
		selected = append(selected, item)
	}
	return selected
}

func encodedSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return maxModelEvidenceBytes
	}
	return len(encoded)
}

func truncateEvidenceText(value string, maximum int) string {
	characters := []rune(strings.TrimSpace(value))
	if len(characters) > maximum {
		characters = characters[:maximum]
	}
	return string(characters)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
