package evidence

import "regexp"

var (
	evidenceIDPattern    = regexp.MustCompile(`\bev_[A-Za-z0-9_-]+\b`)
	failedQueryIDPattern = regexp.MustCompile(`\bfq_[A-Za-z0-9_-]+\b`)
)

type EvidenceSet struct {
	EvidenceIDs    map[string]bool
	FailedQueryIDs map[string]bool
}

type ValidationResult struct {
	OK      bool
	Reasons []string
}

func ExtractIDs(text string) EvidenceSet {
	set := EvidenceSet{
		EvidenceIDs:    map[string]bool{},
		FailedQueryIDs: map[string]bool{},
	}
	for _, id := range evidenceIDPattern.FindAllString(text, -1) {
		set.EvidenceIDs[id] = true
	}
	for _, id := range failedQueryIDPattern.FindAllString(text, -1) {
		set.FailedQueryIDs[id] = true
	}
	return set
}

func Merge(sets ...EvidenceSet) EvidenceSet {
	merged := EvidenceSet{
		EvidenceIDs:    map[string]bool{},
		FailedQueryIDs: map[string]bool{},
	}
	for _, set := range sets {
		for id := range set.EvidenceIDs {
			merged.EvidenceIDs[id] = true
		}
		for id := range set.FailedQueryIDs {
			merged.FailedQueryIDs[id] = true
		}
	}
	return merged
}

// ValidateAnswer only validates identifier references. Coverage, freshness,
// claim type, and language-localized wording are decided before rendering by
// the structured Claim -> Matcher pipeline.
func ValidateAnswer(answer string, available EvidenceSet) ValidationResult {
	answerIDs := ExtractIDs(answer)
	var reasons []string
	for id := range answerIDs.EvidenceIDs {
		if !available.EvidenceIDs[id] {
			reasons = append(reasons, "answer cites unknown evidence_id "+id)
		}
	}
	for id := range answerIDs.FailedQueryIDs {
		if !available.FailedQueryIDs[id] {
			reasons = append(reasons, "answer cites unknown failed_query_id "+id)
		}
	}
	return ValidationResult{OK: len(reasons) == 0, Reasons: reasons}
}
