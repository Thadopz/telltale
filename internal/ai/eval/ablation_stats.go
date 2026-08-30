package eval

import (
	"math"
	"math/rand"
	"sort"
	"strings"
)

const defaultBootstrapSamples = 10000

func SummarizeAblation(results []EvalCaseResult, samples int, seed int64) AblationSummary {
	if samples <= 0 {
		samples = defaultBootstrapSamples
	}
	if seed == 0 {
		seed = 1
	}
	summary := AblationSummary{BootstrapSamples: samples}
	for index, mode := range EvidenceModes() {
		group := ablationGroup(results, mode, samples, seed+int64(index+1))
		summary.Groups = append(summary.Groups, group)
	}

	pairs := [][2]EvidenceMode{
		{EvidenceModeFull, EvidenceModeRawUnenforced},
		{EvidenceModeFull, EvidenceModeVisibleUnenforced},
		{EvidenceModeVisibleUnenforced, EvidenceModeRawUnenforced},
	}
	for index, pair := range pairs {
		summary.Comparisons = append(summary.Comparisons, compareAblationModes(results, pair[0], pair[1], samples, seed+int64(100+index)))
	}
	applyHolmCorrection(summary.Comparisons)
	summary.Strata = ablationStrata(results, samples, seed)
	summary.ErrorCounts = ablationErrorCounts(results)
	summary.Conclusion = ablationConclusion(summary.Comparisons)
	return summary
}

func ablationStrata(results []EvalCaseResult, samples int, seed int64) []AblationStratum {
	dimensions := map[string]map[string][]EvalCaseResult{
		"scenario":              {},
		"evidence_completeness": {},
	}
	for _, result := range results {
		if result.EvidenceMode == "" {
			continue
		}
		dimensions["scenario"][result.ID] = append(dimensions["scenario"][result.ID], result)
		class := evidenceCompletenessClass(result.Category)
		dimensions["evidence_completeness"][class] = append(dimensions["evidence_completeness"][class], result)
	}
	var strata []AblationStratum
	for _, dimension := range []string{"scenario", "evidence_completeness"} {
		values := make([]string, 0, len(dimensions[dimension]))
		for value := range dimensions[dimension] {
			values = append(values, value)
		}
		sort.Strings(values)
		for valueIndex, value := range values {
			stratum := AblationStratum{Dimension: dimension, Value: value}
			for modeIndex, mode := range EvidenceModes() {
				stratum.Groups = append(stratum.Groups, ablationGroup(dimensions[dimension][value], mode, samples, seed+int64(1000+valueIndex*10+modeIndex)))
			}
			strata = append(strata, stratum)
		}
	}
	return strata
}

func evidenceCompletenessClass(category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	switch {
	case strings.Contains(category, "partial"):
		return "partial"
	case strings.Contains(category, "expired"):
		return "stale"
	case strings.Contains(category, "conflict"):
		return "conflict"
	case strings.Contains(category, "timeout"):
		return "tool_failure"
	case strings.Contains(category, "insufficient"), strings.Contains(category, "scope"):
		return "insufficient"
	case strings.Contains(category, "healthy"):
		return "healthy"
	default:
		return "complete"
	}
}

func ablationErrorCounts(results []EvalCaseResult) []AblationErrorCount {
	counts := map[EvidenceMode]map[string]int{}
	for _, result := range results {
		if result.EvidenceMode == "" || result.AblationScore == nil {
			continue
		}
		errorType := "none"
		switch {
		case isBlockedResult(result):
			errorType = "blocked"
		case strings.TrimSpace(result.Error) != "":
			errorType = "execution_error"
		case result.AblationScore.CriticalHallucination:
			errorType = "critical_hallucination"
		case result.AblationScore.WrongAttribution:
			errorType = "wrong_attribution"
		case !result.AblationScore.AbstentionCorrect:
			errorType = "wrong_abstention"
		case !result.AblationScore.CaseCorrect:
			errorType = "incomplete_facts"
		}
		if counts[result.EvidenceMode] == nil {
			counts[result.EvidenceMode] = map[string]int{}
		}
		counts[result.EvidenceMode][errorType]++
	}
	var output []AblationErrorCount
	for _, mode := range EvidenceModes() {
		types := make([]string, 0, len(counts[mode]))
		for errorType := range counts[mode] {
			types = append(types, errorType)
		}
		sort.Strings(types)
		for _, errorType := range types {
			output = append(output, AblationErrorCount{Mode: mode, ErrorType: errorType, Count: counts[mode][errorType]})
		}
	}
	return output
}

func ablationGroup(results []EvalCaseResult, mode EvidenceMode, samples int, seed int64) AblationGroupSummary {
	group := AblationGroupSummary{Mode: mode}
	var accuracy []float64
	var accuracyScore float64
	var factF1 float64
	var hallucinations int
	var abstentions int
	var completed int
	var tokens int
	var latency int64
	for _, result := range results {
		if result.EvidenceMode != mode || result.AblationScore == nil {
			continue
		}
		if isBlockedResult(result) {
			group.Blocked++
			continue
		}
		group.Runs++
		value := 0.0
		if result.AblationScore.CaseCorrect {
			value = 1
		}
		accuracy = append(accuracy, value)
		accuracyScore += result.AblationScore.AccuracyScore
		factF1 += result.AblationScore.FactF1
		if result.AblationScore.CriticalHallucination {
			hallucinations++
		}
		if result.AblationScore.AbstentionCorrect {
			abstentions++
		}
		if result.AblationScore.TaskCompleted {
			completed++
		}
		tokens += result.Metrics.TotalTokens
		latency += result.ElapsedMS
	}
	if group.Runs == 0 {
		return group
	}
	group.Accuracy = mean(accuracy)
	group.MeanAccuracyScore = accuracyScore / float64(group.Runs)
	group.MeanFactF1 = factF1 / float64(group.Runs)
	group.CriticalHallucinationRate = float64(hallucinations) / float64(group.Runs)
	group.CorrectAbstentionRate = float64(abstentions) / float64(group.Runs)
	group.CompletionRate = float64(completed) / float64(group.Runs)
	group.AverageTokens = float64(tokens) / float64(group.Runs)
	group.AverageLatencyMS = float64(latency) / float64(group.Runs)
	group.AccuracyCILow, group.AccuracyCIHigh = bootstrapMeanCI(accuracy, samples, seed)
	return group
}

func compareAblationModes(results []EvalCaseResult, left EvidenceMode, right EvidenceMode, samples int, seed int64) AblationComparison {
	comparison := AblationComparison{Left: left, Right: right, McNemarP: 1, HolmAdjustedP: 1}
	byPair := map[string]map[EvidenceMode]EvalCaseResult{}
	for _, result := range results {
		if result.EvidenceMode != left && result.EvidenceMode != right {
			continue
		}
		if isBlockedResult(result) {
			continue
		}
		if byPair[result.PairID] == nil {
			byPair[result.PairID] = map[EvidenceMode]EvalCaseResult{}
		}
		byPair[result.PairID][result.EvidenceMode] = result
	}
	var differences []float64
	leftOnly := 0
	rightOnly := 0
	for _, pair := range byPair {
		leftResult, leftOK := pair[left]
		rightResult, rightOK := pair[right]
		if !leftOK || !rightOK || leftResult.AblationScore == nil || rightResult.AblationScore == nil {
			comparison.MissingPairs++
			continue
		}
		leftValue := boolFloat(leftResult.AblationScore.CaseCorrect)
		rightValue := boolFloat(rightResult.AblationScore.CaseCorrect)
		differences = append(differences, leftValue-rightValue)
		if leftValue > rightValue {
			leftOnly++
		} else if rightValue > leftValue {
			rightOnly++
		}
	}
	comparison.Pairs = len(differences)
	if len(differences) == 0 {
		return comparison
	}
	comparison.AccuracyDelta = mean(differences)
	comparison.CILow, comparison.CIHigh = bootstrapMeanCI(differences, samples, seed)
	comparison.McNemarP = exactMcNemarP(leftOnly, rightOnly)
	return comparison
}

func bootstrapMeanCI(values []float64, samples int, seed int64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	if len(values) == 1 {
		return values[0], values[0]
	}
	rng := rand.New(rand.NewSource(seed))
	distribution := make([]float64, samples)
	for sample := 0; sample < samples; sample++ {
		total := 0.0
		for index := 0; index < len(values); index++ {
			total += values[rng.Intn(len(values))]
		}
		distribution[sample] = total / float64(len(values))
	}
	sort.Float64s(distribution)
	return percentile(distribution, 0.025), percentile(distribution, 0.975)
}

func exactMcNemarP(leftOnly int, rightOnly int) float64 {
	n := leftOnly + rightOnly
	if n == 0 {
		return 1
	}
	k := leftOnly
	if rightOnly < k {
		k = rightOnly
	}
	probability := math.Pow(0.5, float64(n))
	cumulative := probability
	for index := 1; index <= k; index++ {
		probability *= float64(n-index+1) / float64(index)
		cumulative += probability
	}
	p := 2 * cumulative
	if p > 1 {
		return 1
	}
	return p
}

func applyHolmCorrection(comparisons []AblationComparison) {
	type ranked struct {
		index int
		p     float64
	}
	rankedValues := make([]ranked, len(comparisons))
	for index, comparison := range comparisons {
		rankedValues[index] = ranked{index: index, p: comparison.McNemarP}
	}
	sort.Slice(rankedValues, func(i int, j int) bool { return rankedValues[i].p < rankedValues[j].p })
	previous := 0.0
	for rank, value := range rankedValues {
		adjusted := float64(len(rankedValues)-rank) * value.p
		if adjusted < previous {
			adjusted = previous
		}
		if adjusted > 1 {
			adjusted = 1
		}
		previous = adjusted
		comparisons[value.index].HolmAdjustedP = adjusted
		comparisons[value.index].Significant = adjusted < 0.05
	}
}

func ablationConclusion(comparisons []AblationComparison) string {
	for _, comparison := range comparisons {
		if comparison.Left != EvidenceModeFull || comparison.Right != EvidenceModeRawUnenforced {
			continue
		}
		if comparison.AccuracyDelta >= 0.05 && comparison.CILow > 0 {
			return "evidence_system_material_positive_effect"
		}
		if comparison.CIHigh < 0 {
			return "evidence_system_negative_effect"
		}
		return "inconclusive"
	}
	return "insufficient_pairs"
}

func isBlockedResult(result EvalCaseResult) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Error)), "blocked:")
}

func percentile(values []float64, probability float64) float64 {
	if len(values) == 0 {
		return 0
	}
	position := probability * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
