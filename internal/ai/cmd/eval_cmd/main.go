package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/eval"
	"github.com/Thadopz/telltale/internal/appconfig"
)

func main() {
	configRoot := flag.String("config-root", ".", "project root or config directory; evidence_ablation requires an eval/test runtime config")
	casesPath := flag.String("cases", "", "path to eval cases jsonl; defaults depend on mode")
	caseIDs := flag.String("case-ids", "", "optional comma-separated eval case ids")
	mode := flag.String("mode", string(eval.ModeContract), "eval mode: contract, routing, smoke, context_stress, query_result_pressure, or evidence_ablation")
	reportsDir := flag.String("reports", "reports", "directory for eval reports")
	runID := flag.String("run-id", "", "optional report run id")
	caseTimeout := flag.Int("case-timeout", 60, "per-case timeout in seconds")
	repetitions := flag.Int("repetitions", 0, "runs per case; defaults to 3 for routing, 5 for evidence_ablation, and 1 otherwise")
	minPassRate := flag.Float64("min-pass-rate", 0, "required pass rate from 0 to 1; defaults to 0.9 for routing and 1 otherwise")
	enableJudge := flag.Bool("judge", false, "enable semantic answer judging for cases with judge_criteria")
	realModel := flag.Bool("real-model", false, "use the configured real model for context_stress instead of its deterministic model")
	modelVersion := flag.String("model-version", "", "model version label recorded in the report; defaults to quick_chat_model.model")
	bootstrapSamples := flag.Int("bootstrap-samples", 10000, "paired bootstrap samples for evidence_ablation")
	randomSeed := flag.Int64("random-seed", 1, "reproducible bootstrap and blind-pack shuffle seed")
	humanReview := flag.String("human-review", "", "optional JSONL of blinded human decisions keyed by review_id")
	flag.Parse()
	ctx := context.Background()
	appconfig.ConfigureAt(ctx, *configRoot)
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resolvedCasesPath := *casesPath
	if resolvedCasesPath == "" {
		resolvedCasesPath = defaultCasesPath(eval.Mode(*mode))
	}
	resolvedRepetitions := *repetitions
	if resolvedRepetitions <= 0 {
		resolvedRepetitions = defaultRepetitions(eval.Mode(*mode))
	}

	resolvedRealModel := *realModel || eval.Mode(*mode) == eval.ModeEvidenceAblation
	result, err := eval.Run(ctx, eval.RunOptions{
		Mode:               eval.Mode(*mode),
		CasesPath:          resolvedCasesPath,
		CaseIDs:            splitCaseIDs(*caseIDs),
		ReportsDir:         *reportsDir,
		RunID:              *runID,
		CaseTimeoutSeconds: *caseTimeout,
		Repetitions:        resolvedRepetitions,
		MinimumPassRate:    *minPassRate,
		EnableJudge:        *enableJudge,
		RealModel:          resolvedRealModel,
		ModelVersion:       *modelVersion,
		BootstrapSamples:   *bootstrapSamples,
		RandomSeed:         *randomSeed,
		HumanReviewPath:    *humanReview,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("mode=%s cases=%d attempts=%d passed=%d failed=%d pass_rate=%.2f%% required=%.2f%% gate=%v\n",
		result.Summary.Mode,
		result.Summary.Cases,
		result.Summary.Total,
		result.Summary.Passed,
		result.Summary.Failed,
		result.Summary.PassRate*100,
		result.Summary.RequiredPassRate*100,
		result.Summary.GatePass,
	)
	fmt.Printf("jsonl=%s\nmarkdown=%s\n", result.JSONLPath, result.MarkdownPath)
	if result.BlindReviewPath != "" {
		fmt.Printf("blind_review=%s\n", result.BlindReviewPath)
	}
	if !result.Summary.GatePass {
		os.Exit(2)
	}
}

func splitCaseIDs(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func defaultCasesPath(mode eval.Mode) string {
	switch mode {
	case eval.ModeRouting:
		return "testdata/eval/routing"
	case eval.ModeSmoke:
		return "testdata/eval/smoke"
	case eval.ModeContextStress:
		return "testdata/eval/context_stress"
	case eval.ModeQueryResultPressure:
		return "testdata/eval/query_result_pressure"
	case eval.ModeEvidenceAblation:
		return "testdata/eval/evidence_ablation"
	default:
		return "testdata/eval/contract"
	}
}

func defaultRepetitions(mode eval.Mode) int {
	if mode == eval.ModeRouting {
		return 3
	}
	if mode == eval.ModeEvidenceAblation {
		return 5
	}
	return 1
}
