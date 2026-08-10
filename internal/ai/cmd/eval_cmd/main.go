package main

import (
	"github.com/Thadopz/telltale/internal/ai/eval"
	"github.com/Thadopz/telltale/internal/appconfig"
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	casesPath := flag.String("cases", "", "path to eval cases jsonl; defaults depend on mode")
	mode := flag.String("mode", string(eval.ModeContract), "eval mode: contract, routing, smoke, context_stress, or query_result_pressure")
	reportsDir := flag.String("reports", "reports", "directory for eval reports")
	runID := flag.String("run-id", "", "optional report run id")
	caseTimeout := flag.Int("case-timeout", 60, "per-case timeout in seconds")
	repetitions := flag.Int("repetitions", 0, "runs per case; defaults to 3 for routing and 1 otherwise")
	minPassRate := flag.Float64("min-pass-rate", 0, "required pass rate from 0 to 1; defaults to 0.9 for routing and 1 otherwise")
	enableJudge := flag.Bool("judge", false, "enable semantic answer judging for cases with judge_criteria")
	realModel := flag.Bool("real-model", false, "use the configured real model for context_stress instead of its deterministic model")
	flag.Parse()
	ctx := context.Background()
	appconfig.Configure(ctx)
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

	result, err := eval.Run(ctx, eval.RunOptions{
		Mode:               eval.Mode(*mode),
		CasesPath:          resolvedCasesPath,
		ReportsDir:         *reportsDir,
		RunID:              *runID,
		CaseTimeoutSeconds: *caseTimeout,
		Repetitions:        resolvedRepetitions,
		MinimumPassRate:    *minPassRate,
		EnableJudge:        *enableJudge,
		RealModel:          *realModel,
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
	if !result.Summary.GatePass {
		os.Exit(2)
	}
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
	default:
		return "testdata/eval/contract"
	}
}

func defaultRepetitions(mode eval.Mode) int {
	if mode == eval.ModeRouting {
		return 3
	}
	return 1
}
