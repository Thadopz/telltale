package eval

import (
	"regexp"
	"sort"
	"strings"
)

var blindEvidenceIDPattern = regexp.MustCompile(`(?i)\bev_[a-z0-9_-]+\b`)

// BlindEvaluationText removes experiment-only markers before normalization or
// semantic judging. It does not otherwise rewrite the answer.
func BlindEvaluationText(answer string) string {
	answer = blindEvidenceIDPattern.ReplaceAllString(answer, "[evidence]")
	replacements := []string{
		string(EvidenceModeVisibleUnenforced),
		string(EvidenceModeRawUnenforced),
		string(EvidenceModeFull),
		"coverage_status",
		"coverage status",
	}
	for _, replacement := range replacements {
		answer = strings.ReplaceAll(answer, replacement, "[redacted]")
		answer = strings.ReplaceAll(answer, strings.ToUpper(replacement), "[redacted]")
	}
	return answer
}

// normalizeAblationClaims is the deterministic, reproducible primary
// normalizer. It sees only a blinded answer and evaluator-owned atomic aliases;
// it never sees the evidence mode, Evidence IDs, or coverage reports.
func normalizeAblationClaims(evalCase Case, result EvalCaseResult) NormalizedAblationClaims {
	normalized := NormalizedAblationClaims{
		Normalizer: "deterministic_atomic_v1",
		Completed:  strings.TrimSpace(result.Answer) != "" && strings.TrimSpace(result.Error) == "",
	}
	if evalCase.Truth == nil {
		normalized.NormalizerError = "missing atomic truth"
		return normalized
	}
	answer := strings.ToLower(BlindEvaluationText(result.Answer))
	normalized.Abstained = containsAnyAblation(answer, []string{
		"insufficient evidence", "not enough evidence", "cannot determine", "unable to determine",
		"evidence gap", "unverified", "无法确定", "证据不足", "缺少证据", "不能下结论",
	})

	rootAliases := append([]string{evalCase.Truth.RootCause}, evalCase.Truth.RootCauseMatchAny...)
	if containsAnyAblation(answer, rootAliases) {
		normalized.RootCause = evalCase.Truth.RootCause
	} else if normalized.Abstained {
		normalized.RootCause = "insufficient_evidence"
	} else if normalized.Completed {
		normalized.RootCause = "other"
	}

	for _, fact := range evalCase.Truth.RequiredFacts {
		aliases := append([]string{fact.Description}, fact.MatchAny...)
		if containsAnyAblation(answer, aliases) {
			normalized.ClaimedFactIDs = append(normalized.ClaimedFactIDs, fact.ID)
		}
	}
	for _, claim := range evalCase.Truth.ForbiddenClaims {
		aliases := append([]string{claim.Description}, claim.MatchAny...)
		quote, asserted := findAffirmativeAblationClaim(BlindEvaluationText(result.Answer), aliases)
		normalized.ForbiddenAssessments = append(normalized.ForbiddenAssessments, ForbiddenClaimAssessment{
			ForbiddenID:     claim.ID,
			Asserted:        asserted,
			SupportingQuote: quote,
			Polarity:        fallbackPolarity(asserted),
			Accepted:        asserted,
			RejectionReason: fallbackRejectionReason(asserted),
		})
		if asserted {
			normalized.ForbiddenIDs = append(normalized.ForbiddenIDs, claim.ID)
		}
	}
	sort.Strings(normalized.ClaimedFactIDs)
	sort.Strings(normalized.ForbiddenIDs)
	return normalized
}

func findAffirmativeAblationClaim(answer string, aliases []string) (string, bool) {
	lowerAnswer := strings.ToLower(answer)
	for _, alias := range aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias == "" {
			continue
		}
		searchFrom := 0
		for searchFrom <= len(lowerAnswer)-len(alias) {
			relativeIndex := strings.Index(lowerAnswer[searchFrom:], alias)
			if relativeIndex < 0 {
				break
			}
			quoteStart := searchFrom + relativeIndex
			quoteEnd := quoteStart + len(alias)
			if !containsNonAffirmativeMarker(quoteStatementContext(lowerAnswer, quoteStart, quoteEnd)) {
				return answer[quoteStart:quoteEnd], true
			}
			searchFrom = quoteEnd
		}
	}
	return "", false
}

func fallbackPolarity(asserted bool) string {
	if asserted {
		return "affirmed"
	}
	return "not_mentioned_or_non_affirmative"
}

func fallbackRejectionReason(asserted bool) string {
	if asserted {
		return ""
	}
	return "no_affirmative_alias_match"
}

func scoreAblationCase(evalCase Case, result EvalCaseResult, claims NormalizedAblationClaims) AblationCaseScore {
	taskCompleted := claims.Completed && strings.TrimSpace(result.Error) == ""
	score := AblationCaseScore{TaskCompleted: taskCompleted}
	if evalCase.Truth == nil {
		return score
	}
	truth := evalCase.Truth
	score.RootCauseCorrect = claims.RootCause == truth.RootCause
	if truth.ExpectedAbstention && claims.Abstained {
		score.RootCauseCorrect = truth.RootCause == "" || truth.RootCause == "insufficient_evidence"
	}

	required := make(map[string]bool, len(truth.RequiredFacts))
	for _, fact := range truth.RequiredFacts {
		required[fact.ID] = true
	}
	matched := 0
	for _, id := range claims.ClaimedFactIDs {
		if required[id] {
			matched++
		}
	}
	if len(required) == 0 {
		score.FactRecall = 1
	} else {
		score.FactRecall = float64(matched) / float64(len(required))
	}
	claimCount := len(claims.ClaimedFactIDs) + len(claims.ForbiddenIDs)
	if claimCount == 0 {
		if len(required) == 0 {
			score.FactPrecision = 1
		}
	} else {
		score.FactPrecision = float64(matched) / float64(claimCount)
	}
	if score.FactPrecision+score.FactRecall > 0 {
		score.FactF1 = 2 * score.FactPrecision * score.FactRecall / (score.FactPrecision + score.FactRecall)
	}

	critical := map[string]bool{}
	for _, claim := range truth.ForbiddenClaims {
		if claim.Critical {
			critical[claim.ID] = true
		}
	}
	for _, id := range claims.ForbiddenIDs {
		if critical[id] {
			score.CriticalHallucination = true
			break
		}
	}
	score.WrongAttribution = claims.Completed && !claims.Abstained && !score.RootCauseCorrect
	score.AbstentionCorrect = claims.Abstained == truth.ExpectedAbstention
	score.ToolSelectionCorrect = expectedToolsUsed(evalCase.Expected, result.ToolCalls)

	noCriticalHallucination := 0.0
	if !score.CriticalHallucination {
		noCriticalHallucination = 1
	}
	abstention := 0.0
	if score.AbstentionCorrect {
		abstention = 1
	}
	root := 0.0
	if score.RootCauseCorrect {
		root = 1
	}
	score.AccuracyScore = 0.40*root + 0.30*score.FactF1 + 0.20*noCriticalHallucination + 0.10*abstention
	score.CaseCorrect = score.TaskCompleted && score.RootCauseCorrect && score.FactRecall >= 0.80 && !score.CriticalHallucination && score.AbstentionCorrect
	return score
}

func expectedToolsUsed(expected Expected, calls []ToolEvidence) bool {
	used := map[string]bool{}
	for _, call := range calls {
		used[call.Name] = true
	}
	for _, name := range expected.RequiredTools {
		if !used[name] {
			return false
		}
	}
	if len(expected.RequiredToolsAny) > 0 {
		found := false
		for _, name := range expected.RequiredToolsAny {
			if used[name] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, name := range expected.ForbiddenTools {
		if used[name] {
			return false
		}
	}
	return true
}

func containsAnyAblation(text string, values []string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}
