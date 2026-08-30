package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Summarize(mode Mode, results []EvalCaseResult) RunSummary {
	summary := RunSummary{
		Mode:           mode,
		Total:          len(results),
		ToolCallCounts: map[string]int{},
		CasePassRates:  map[string]float64{},
	}
	reasonSet := map[string]bool{}
	caseAttempts := map[string]int{}
	casePasses := map[string]int{}
	totalModelCalls := 0
	totalToolCalls := 0
	totalTokens := 0
	totalRouteScore := 0.0
	totalToolScore := 0.0
	totalEvidenceScore := 0.0
	totalClaimScore := 0.0
	totalAnswerScore := 0.0
	totalSafetyScore := 0.0
	for _, result := range results {
		caseKey := resultCaseKey(result)
		caseAttempts[caseKey]++
		if strings.Contains(strings.ToLower(result.Category), "safety") {
			summary.SafetyAttempts++
			if result.Scores.Pass {
				summary.SafetyAttemptsPassed++
			}
		}
		if result.Scores.Pass {
			summary.Passed++
			casePasses[caseKey]++
		} else {
			summary.Failed++
		}
		if result.Context.UseKnowledge {
			summary.KnowledgeUsed++
		}
		totalModelCalls += result.Metrics.ModelCalls
		totalToolCalls += result.Metrics.ToolCalls
		totalTokens += result.Metrics.TotalTokens
		totalRouteScore += result.Scores.RouteScore
		totalToolScore += result.Scores.ToolScore
		totalEvidenceScore += result.Scores.EvidenceScore
		totalClaimScore += result.Scores.ClaimScore
		totalAnswerScore += result.Scores.AnswerScore
		totalSafetyScore += result.Scores.SafetyScore
		for _, toolCall := range result.ToolCalls {
			if toolCall.Name != "" {
				summary.ToolCallCounts[toolCall.Name]++
			}
		}
		for _, reason := range result.Scores.Reasons {
			if !reasonSet[reason] {
				reasonSet[reason] = true
				summary.FailureReasons = append(summary.FailureReasons, reason)
			}
		}
	}
	if summary.Total > 0 {
		summary.PassRate = float64(summary.Passed) / float64(summary.Total)
		summary.AverageModelCalls = float64(totalModelCalls) / float64(summary.Total)
		summary.AverageToolCalls = float64(totalToolCalls) / float64(summary.Total)
		summary.AverageTotalTokens = float64(totalTokens) / float64(summary.Total)
		summary.AverageRouteScore = totalRouteScore / float64(summary.Total)
		summary.AverageToolScore = totalToolScore / float64(summary.Total)
		summary.AverageEvidenceScore = totalEvidenceScore / float64(summary.Total)
		summary.AverageClaimScore = totalClaimScore / float64(summary.Total)
		summary.AverageAnswerScore = totalAnswerScore / float64(summary.Total)
		summary.AverageSafetyScore = totalSafetyScore / float64(summary.Total)
	}
	summary.Cases = len(caseAttempts)
	for caseID, attempts := range caseAttempts {
		passRate := float64(casePasses[caseID]) / float64(attempts)
		summary.CasePassRates[caseID] = passRate
		if passRate == 1 {
			summary.StableCases++
		}
	}
	sort.Strings(summary.FailureReasons)
	return summary
}

func WriteReports(results []EvalCaseResult, opts RunOptions) (RunResult, error) {
	if opts.ReportsDir == "" {
		opts.ReportsDir = "reports"
	}
	if opts.RunID == "" {
		opts.RunID = "eval"
	}
	if err := os.MkdirAll(opts.ReportsDir, 0755); err != nil {
		return RunResult{}, err
	}

	jsonlPath := filepath.Join(opts.ReportsDir, "eval-"+opts.RunID+".jsonl")
	mdPath := filepath.Join(opts.ReportsDir, "eval-"+opts.RunID+".md")
	if err := writeJSONL(jsonlPath, results); err != nil {
		return RunResult{}, err
	}
	summary := Summarize(opts.Mode, results)
	if opts.Mode == ModeEvidenceAblation {
		ablation := SummarizeAblation(results, opts.BootstrapSamples, opts.RandomSeed)
		if strings.TrimSpace(opts.HumanReviewPath) != "" {
			reviews, err := LoadHumanReviews(opts.HumanReviewPath)
			if err != nil {
				return RunResult{}, fmt.Errorf("load human reviews: %w", err)
			}
			agreement := CalculateReviewerAgreement(results, reviews)
			ablation.ReviewerAgreement = &agreement
		}
		summary.Ablation = &ablation
	}
	applyQualityGate(&summary, opts.MinimumPassRate)
	if err := os.WriteFile(mdPath, []byte(markdownReport(summary, results)), 0644); err != nil {
		return RunResult{}, err
	}
	blindReviewPath := ""
	if opts.Mode == ModeEvidenceAblation {
		blindReviewPath = filepath.Join(opts.ReportsDir, "eval-"+opts.RunID+"-blind-review.jsonl")
		reviewFraction := 0.10
		if summary.Ablation != nil && summary.Ablation.ReviewerAgreement != nil {
			reviewFraction = summary.Ablation.ReviewerAgreement.RecommendedReviewFraction
		}
		if err := writeBlindReviewPack(blindReviewPath, results, opts.RandomSeed, reviewFraction); err != nil {
			return RunResult{}, err
		}
	}
	return RunResult{Summary: summary, Results: results, JSONLPath: jsonlPath, MarkdownPath: mdPath, BlindReviewPath: blindReviewPath}, nil
}

func applyQualityGate(summary *RunSummary, minimumPassRate float64) {
	if summary.Mode == ModeEvidenceAblation {
		summary.RequiredPassRate = 0
		summary.SafetyGatePass = true
		summary.VariantGatePass = summary.Ablation != nil && len(summary.Ablation.Groups) == len(EvidenceModes())
		if summary.Ablation != nil {
			for _, group := range summary.Ablation.Groups {
				if group.Runs == 0 {
					summary.VariantGatePass = false
				}
			}
			for _, comparison := range summary.Ablation.Comparisons {
				if comparison.Pairs == 0 {
					summary.VariantGatePass = false
				}
			}
		}
		summary.GatePass = summary.VariantGatePass
		return
	}
	summary.RequiredPassRate = minimumPassRate
	if summary.RequiredPassRate <= 0 {
		summary.RequiredPassRate = DefaultMinimumPassRate(summary.Mode)
	}
	summary.SafetyGatePass = summary.SafetyAttempts == summary.SafetyAttemptsPassed
	summary.VariantGatePass = true
	if summary.Mode == ModeContextStress {
		for _, passRate := range summary.CasePassRates {
			if passRate < summary.RequiredPassRate {
				summary.VariantGatePass = false
				break
			}
		}
	}
	summary.GatePass = summary.PassRate >= summary.RequiredPassRate && summary.SafetyGatePass && summary.VariantGatePass
}

type blindReviewEntry struct {
	ReviewID               string `json:"review_id"`
	CaseID                 string `json:"case_id"`
	Attempt                int    `json:"attempt"`
	Query                  string `json:"query"`
	Answer                 string `json:"answer"`
	RecommendedHumanReview bool   `json:"recommended_human_review"`
}

func writeBlindReviewPack(path string, results []EvalCaseResult, seed int64, reviewFraction float64) error {
	entries := make([]blindReviewEntry, 0, len(results))
	for _, result := range results {
		if result.EvidenceMode == "" {
			continue
		}
		entries = append(entries, blindReviewEntry{
			ReviewID: result.ReviewID,
			CaseID:   result.ID,
			Attempt:  result.Attempt,
			Query:    result.Query,
			Answer:   BlindEvaluationText(result.Answer),
		})
	}
	// A fixed shuffle prevents the original balanced arm order from revealing
	// the hidden group while keeping review packs reproducible.
	if seed == 0 {
		seed = 1
	}
	random := rand.New(rand.NewSource(seed))
	random.Shuffle(len(entries), func(i int, j int) { entries[i], entries[j] = entries[j], entries[i] })
	reviewCount := int(math.Ceil(float64(len(entries)) * reviewFraction))
	for index := 0; index < reviewCount && index < len(entries); index++ {
		entries[index].RecommendedHumanReview = true
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func writeJSONL(path string, results []EvalCaseResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, result := range results {
		if err := enc.Encode(result); err != nil {
			return err
		}
	}
	return w.Flush()
}

func markdownReport(summary RunSummary, results []EvalCaseResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent Eval Report\n\n")
	fmt.Fprintf(&b, "- Mode: `%s`\n", summary.Mode)
	fmt.Fprintf(&b, "- Cases: %d\n", summary.Cases)
	fmt.Fprintf(&b, "- Stable cases: %d\n", summary.StableCases)
	fmt.Fprintf(&b, "- Total: %d\n", summary.Total)
	fmt.Fprintf(&b, "- Passed: %d\n", summary.Passed)
	fmt.Fprintf(&b, "- Failed: %d\n", summary.Failed)
	fmt.Fprintf(&b, "- Pass rate: %.2f%%\n", summary.PassRate*100)
	fmt.Fprintf(&b, "- Required pass rate: %.2f%%\n", summary.RequiredPassRate*100)
	fmt.Fprintf(&b, "- Quality gate: `%v`\n", summary.GatePass)
	fmt.Fprintf(&b, "- Safety gate: `%v` (%d/%d)\n", summary.SafetyGatePass, summary.SafetyAttemptsPassed, summary.SafetyAttempts)
	if summary.Mode == ModeContextStress {
		fmt.Fprintf(&b, "- Variant gate: `%v`\n", summary.VariantGatePass)
	}
	fmt.Fprintf(&b, "- Knowledge used: %d\n\n", summary.KnowledgeUsed)
	fmt.Fprintf(&b, "- Average model calls: %.2f\n", summary.AverageModelCalls)
	fmt.Fprintf(&b, "- Average tool calls: %.2f\n\n", summary.AverageToolCalls)
	fmt.Fprintf(&b, "- Average total tokens: %.2f\n\n", summary.AverageTotalTokens)
	fmt.Fprintf(&b, "- Average scores: route=%.2f tool=%.2f evidence=%.2f claim=%.2f answer=%.2f safety=%.2f\n\n",
		summary.AverageRouteScore,
		summary.AverageToolScore,
		summary.AverageEvidenceScore,
		summary.AverageClaimScore,
		summary.AverageAnswerScore,
		summary.AverageSafetyScore,
	)
	if summary.Ablation != nil {
		fmt.Fprintf(&b, "## Evidence Ablation\n\n")
		fmt.Fprintf(&b, "- Bootstrap samples: %d\n", summary.Ablation.BootstrapSamples)
		fmt.Fprintf(&b, "- Conclusion: `%s`\n\n", summary.Ablation.Conclusion)
		if agreement := summary.Ablation.ReviewerAgreement; agreement != nil {
			fmt.Fprintf(&b, "- Human/Judge agreement: reviewed=%d/%d kappa=%.3f reliable=%v recommended_review=%.0f%%\n\n",
				agreement.Reviewed, agreement.Eligible, agreement.CohensKappa,
				agreement.Reliable, agreement.RecommendedReviewFraction*100)
		}
		fmt.Fprintf(&b, "| Mode | Runs | Accuracy (95%% CI) | Fact F1 | Hallucination | Completion | Tokens | Latency |\n")
		fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, group := range summary.Ablation.Groups {
			fmt.Fprintf(&b, "| `%s` | %d | %.2f%% [%.2f%%, %.2f%%] | %.3f | %.2f%% | %.2f%% | %.1f | %.1fms |\n",
				group.Mode, group.Runs, group.Accuracy*100, group.AccuracyCILow*100, group.AccuracyCIHigh*100,
				group.MeanFactF1, group.CriticalHallucinationRate*100, group.CompletionRate*100,
				group.AverageTokens, group.AverageLatencyMS)
		}
		fmt.Fprintf(&b, "\n| Comparison | Pairs | Accuracy delta (95%% CI) | McNemar p | Holm p | Significant |\n")
		fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|\n")
		for _, comparison := range summary.Ablation.Comparisons {
			fmt.Fprintf(&b, "| `%s - %s` | %d | %.2f%% [%.2f%%, %.2f%%] | %.4f | %.4f | %v |\n",
				comparison.Left, comparison.Right, comparison.Pairs, comparison.AccuracyDelta*100,
				comparison.CILow*100, comparison.CIHigh*100, comparison.McNemarP,
				comparison.HolmAdjustedP, comparison.Significant)
		}
		fmt.Fprintf(&b, "\n")
		if len(summary.Ablation.Strata) > 0 {
			fmt.Fprintf(&b, "### Stratified accuracy\n\n")
			fmt.Fprintf(&b, "| Dimension | Value | Full | Visible | Raw |\n")
			fmt.Fprintf(&b, "|---|---|---:|---:|---:|\n")
			for _, stratum := range summary.Ablation.Strata {
				accuracies := map[EvidenceMode]float64{}
				for _, group := range stratum.Groups {
					accuracies[group.Mode] = group.Accuracy
				}
				fmt.Fprintf(&b, "| `%s` | `%s` | %.2f%% | %.2f%% | %.2f%% |\n",
					stratum.Dimension, stratum.Value,
					accuracies[EvidenceModeFull]*100,
					accuracies[EvidenceModeVisibleUnenforced]*100,
					accuracies[EvidenceModeRawUnenforced]*100)
			}
			fmt.Fprintf(&b, "\n")
		}
		if len(summary.Ablation.ErrorCounts) > 0 {
			fmt.Fprintf(&b, "### Error types\n\n")
			for _, count := range summary.Ablation.ErrorCounts {
				fmt.Fprintf(&b, "- `%s/%s`: %d\n", count.Mode, count.ErrorType, count.Count)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	if len(summary.ToolCallCounts) > 0 {
		fmt.Fprintf(&b, "## Tool Calls\n\n")
		names := make([]string, 0, len(summary.ToolCallCounts))
		for name := range summary.ToolCallCounts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "- `%s`: %d\n", name, summary.ToolCallCounts[name])
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(summary.CasePassRates) > 0 {
		fmt.Fprintf(&b, "## Case Stability\n\n")
		caseIDs := make([]string, 0, len(summary.CasePassRates))
		for caseID := range summary.CasePassRates {
			caseIDs = append(caseIDs, caseID)
		}
		sort.Strings(caseIDs)
		for _, caseID := range caseIDs {
			fmt.Fprintf(&b, "- `%s`: %.2f%%\n", caseID, summary.CasePassRates[caseID]*100)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Cases\n\n")
	for _, result := range results {
		status := "PASS"
		if !result.Scores.Pass {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "### %s `%s`\n\n", status, result.ID)
		if result.Variant != "" {
			fmt.Fprintf(&b, "- Variant: `%s`\n", result.Variant)
		}
		if result.EvidenceMode != "" {
			fmt.Fprintf(&b, "- Evidence mode: `%s`\n", result.EvidenceMode)
			fmt.Fprintf(&b, "- Pair: `%s`\n", result.PairID)
			fmt.Fprintf(&b, "- Payload: canonical=%s visible=%s bytes=%d estimated_tokens=%d\n",
				result.CanonicalPayloadDigest, result.ModelVisiblePayloadDigest,
				result.ModelVisiblePayloadBytes, result.ModelVisiblePayloadTokens)
			if result.AblationScore != nil {
				fmt.Fprintf(&b, "- Ablation score: correct=%v continuous=%.3f fact_f1=%.3f hallucination=%v abstention=%v\n",
					result.AblationScore.CaseCorrect, result.AblationScore.AccuracyScore,
					result.AblationScore.FactF1, result.AblationScore.CriticalHallucination,
					result.AblationScore.AbstentionCorrect)
			}
		}
		fmt.Fprintf(&b, "- Category: `%s`\n", result.Category)
		fmt.Fprintf(&b, "- Attempt: %d\n", result.Attempt)
		fmt.Fprintf(&b, "- Knowledge: `%v`\n", result.Context.UseKnowledge)
		fmt.Fprintf(&b, "- Tools: `%s`\n", strings.Join(toolNames(result.ToolCalls), ", "))
		fmt.Fprintf(&b, "- Scores: route=%.0f tool=%.0f evidence=%.0f claim=%.0f answer=%.0f safety=%.0f\n",
			result.Scores.RouteScore,
			result.Scores.ToolScore,
			result.Scores.EvidenceScore,
			result.Scores.ClaimScore,
			result.Scores.AnswerScore,
			result.Scores.SafetyScore,
		)
		fmt.Fprintf(&b, "- Calls: model=%d model_errors=%d agent=%d tool=%d tokens=%d\n",
			result.Metrics.ModelCalls,
			result.Metrics.ModelErrors,
			result.Metrics.AgentCalls,
			result.Metrics.ToolCalls,
			result.Metrics.TotalTokens,
		)
		fmt.Fprintf(&b, "- Context budget: estimated=%d final=%d window=%d overhead=%d safety=%d trimmed=%s\n",
			result.Context.EstimatedPromptTokens,
			result.Context.FinalBudgetTokens,
			result.Context.ContextWindowTokens,
			result.Context.PromptOverheadTokens,
			result.Context.SafetyMarginTokens,
			strings.Join(result.Context.TrimmedLayers, ", "),
		)
		if result.Context.RegisteredToolCount > 0 {
			fmt.Fprintf(&b, "- Context stress: registered_tools=%d critical_facts=%d/%d distractor_calls=%s\n",
				result.Context.RegisteredToolCount,
				len(result.Context.CriticalFactsSeen),
				len(result.Context.CriticalFacts),
				strings.Join(result.Context.DistractorToolCalls, ", "),
			)
		}
		fmt.Fprintf(&b, "- Elapsed: %dms\n", result.ElapsedMS)
		if result.Judge.Enabled {
			fmt.Fprintf(&b, "- Judge: pass=%v score=%.2f tokens=%d reason=%s\n", result.Judge.Pass, result.Judge.Score, result.Judge.TotalTokens, result.Judge.Reason)
			if result.Judge.Error != "" {
				fmt.Fprintf(&b, "- Judge error: %s\n", result.Judge.Error)
			}
		}
		if result.Error != "" {
			fmt.Fprintf(&b, "- Error: %s\n", result.Error)
		}
		if len(result.Scores.Reasons) > 0 {
			fmt.Fprintf(&b, "- Reasons: %s\n", strings.Join(result.Scores.Reasons, "; "))
		}
		fmt.Fprintf(&b, "- Answer: %s\n\n", truncateText(result.Answer, 400))
	}
	return b.String()
}

func resultCaseKey(result EvalCaseResult) string {
	if result.EvidenceMode != "" {
		return result.ID + "/" + string(result.EvidenceMode)
	}
	if result.Variant == "" {
		return result.ID
	}
	return result.ID + "/" + result.Variant
}

func toolNames(calls []ToolEvidence) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.Name != "" {
			names = append(names, call.Name)
		}
	}
	return names
}

func truncateText(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
