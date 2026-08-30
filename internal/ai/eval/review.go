package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type HumanReview struct {
	ReviewID    string `json:"review_id"`
	CaseCorrect bool   `json:"case_correct"`
}

func LoadHumanReviews(path string) ([]HumanReview, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var reviews []HumanReview
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		content := strings.TrimSpace(scanner.Text())
		if content == "" {
			continue
		}
		var review HumanReview
		if err := json.Unmarshal([]byte(content), &review); err != nil {
			return nil, fmt.Errorf("human review line %d: %w", line, err)
		}
		review.ReviewID = strings.TrimSpace(review.ReviewID)
		if review.ReviewID == "" {
			return nil, fmt.Errorf("human review line %d is missing review_id", line)
		}
		if seen[review.ReviewID] {
			return nil, fmt.Errorf("duplicate human review_id %q", review.ReviewID)
		}
		seen[review.ReviewID] = true
		reviews = append(reviews, review)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return reviews, nil
}

func CalculateReviewerAgreement(results []EvalCaseResult, reviews []HumanReview) ReviewerAgreement {
	agreement := ReviewerAgreement{RecommendedReviewFraction: 0.10}
	byID := make(map[string]HumanReview, len(reviews))
	for _, review := range reviews {
		byID[review.ReviewID] = review
	}
	judgeYes := 0
	humanYes := 0
	agree := 0
	for _, result := range results {
		if result.EvidenceMode == "" || !result.Judge.Enabled {
			continue
		}
		agreement.Eligible++
		review, ok := byID[result.ReviewID]
		if !ok {
			continue
		}
		agreement.Reviewed++
		if result.Judge.Pass {
			judgeYes++
		}
		if review.CaseCorrect {
			humanYes++
		}
		if result.Judge.Pass == review.CaseCorrect {
			agree++
		}
	}
	if agreement.Eligible > 0 {
		agreement.ReviewFraction = float64(agreement.Reviewed) / float64(agreement.Eligible)
	}
	if agreement.Reviewed == 0 {
		return agreement
	}
	n := float64(agreement.Reviewed)
	observed := float64(agree) / n
	judgePositive := float64(judgeYes) / n
	humanPositive := float64(humanYes) / n
	expected := judgePositive*humanPositive + (1-judgePositive)*(1-humanPositive)
	if expected == 1 {
		if observed == 1 {
			agreement.CohensKappa = 1
		}
	} else {
		agreement.CohensKappa = (observed - expected) / (1 - expected)
	}
	agreement.Reliable = agreement.CohensKappa >= 0.70
	if !agreement.Reliable {
		agreement.RecommendedReviewFraction = 0.25
	}
	return agreement
}
