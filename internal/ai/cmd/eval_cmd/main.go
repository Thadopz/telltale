package main

import (
	"SREagent/internal/ai/eval"
	"SREagent/internal/appconfig"
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	casesPath := flag.String("cases", "", "path to eval cases jsonl; defaults depend on mode")
	mode := flag.String("mode", string(eval.ModeMock), "eval mode: mock or smoke")
	reportsDir := flag.String("reports", "reports", "directory for eval reports")
	runID := flag.String("run-id", "", "optional report run id")
	caseTimeout := flag.Int("case-timeout", 60, "per-case timeout in seconds")
	flag.Parse()
	ctx := context.Background()
	appconfig.Configure(ctx)
	resolvedCasesPath := *casesPath
	if resolvedCasesPath == "" {
		resolvedCasesPath = defaultCasesPath(eval.Mode(*mode))
	}

	result, err := eval.Run(ctx, eval.RunOptions{
		Mode:               eval.Mode(*mode),
		CasesPath:          resolvedCasesPath,
		ReportsDir:         *reportsDir,
		RunID:              *runID,
		CaseTimeoutSeconds: *caseTimeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("mode=%s total=%d passed=%d failed=%d pass_rate=%.2f%%\n",
		result.Summary.Mode,
		result.Summary.Total,
		result.Summary.Passed,
		result.Summary.Failed,
		result.Summary.PassRate*100,
	)
	fmt.Printf("jsonl=%s\nmarkdown=%s\n", result.JSONLPath, result.MarkdownPath)
	if result.Summary.Failed > 0 {
		os.Exit(2)
	}
}

func defaultCasesPath(mode eval.Mode) string {
	if mode == eval.ModeSmoke {
		return "testdata/eval/smoke_cases.jsonl"
	}
	return "testdata/eval/chat_cases.jsonl"
}
