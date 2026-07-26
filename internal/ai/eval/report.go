package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	totalAnswerScore := 0.0
	totalSafetyScore := 0.0
	for _, result := range results {
		caseAttempts[result.ID]++
		if strings.Contains(strings.ToLower(result.Category), "safety") {
			summary.SafetyAttempts++
			if result.Scores.Pass {
				summary.SafetyAttemptsPassed++
			}
		}
		if result.Scores.Pass {
			summary.Passed++
			casePasses[result.ID]++
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
	applyQualityGate(&summary, opts.MinimumPassRate)
	if err := os.WriteFile(mdPath, []byte(markdownReport(summary, results)), 0644); err != nil {
		return RunResult{}, err
	}
	return RunResult{Summary: summary, Results: results, JSONLPath: jsonlPath, MarkdownPath: mdPath}, nil
}

func applyQualityGate(summary *RunSummary, minimumPassRate float64) {
	summary.RequiredPassRate = minimumPassRate
	if summary.RequiredPassRate <= 0 {
		summary.RequiredPassRate = DefaultMinimumPassRate(summary.Mode)
	}
	summary.SafetyGatePass = summary.SafetyAttempts == summary.SafetyAttemptsPassed
	summary.GatePass = summary.PassRate >= summary.RequiredPassRate && summary.SafetyGatePass
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
	fmt.Fprintf(&b, "- Knowledge used: %d\n\n", summary.KnowledgeUsed)
	fmt.Fprintf(&b, "- Average model calls: %.2f\n", summary.AverageModelCalls)
	fmt.Fprintf(&b, "- Average tool calls: %.2f\n\n", summary.AverageToolCalls)
	fmt.Fprintf(&b, "- Average total tokens: %.2f\n\n", summary.AverageTotalTokens)
	fmt.Fprintf(&b, "- Average scores: route=%.2f tool=%.2f evidence=%.2f answer=%.2f safety=%.2f\n\n",
		summary.AverageRouteScore,
		summary.AverageToolScore,
		summary.AverageEvidenceScore,
		summary.AverageAnswerScore,
		summary.AverageSafetyScore,
	)

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
		fmt.Fprintf(&b, "- Category: `%s`\n", result.Category)
		fmt.Fprintf(&b, "- Attempt: %d\n", result.Attempt)
		fmt.Fprintf(&b, "- Knowledge: `%v`\n", result.Context.UseKnowledge)
		fmt.Fprintf(&b, "- Tools: `%s`\n", strings.Join(toolNames(result.ToolCalls), ", "))
		fmt.Fprintf(&b, "- Scores: route=%.0f tool=%.0f evidence=%.0f answer=%.0f safety=%.0f\n",
			result.Scores.RouteScore,
			result.Scores.ToolScore,
			result.Scores.EvidenceScore,
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
