package eval

import (
	"context"
	"fmt"
	"time"
)

func Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	if opts.Mode == "" {
		opts.Mode = ModeContract
	}
	if opts.ReportsDir == "" {
		opts.ReportsDir = "reports"
	}
	if opts.RunID == "" {
		opts.RunID = time.Now().Format("20060102-150405")
	}
	if opts.CaseTimeoutSeconds <= 0 {
		opts.CaseTimeoutSeconds = 60
	}
	if opts.Repetitions <= 0 {
		opts.Repetitions = 1
	}
	if opts.MinimumPassRate < 0 || opts.MinimumPassRate > 1 {
		return RunResult{}, fmt.Errorf("minimum pass rate must be between 0 and 1")
	}
	if opts.MinimumPassRate == 0 {
		opts.MinimumPassRate = DefaultMinimumPassRate(opts.Mode)
	}
	cases, err := LoadCases(opts.CasesPath)
	if err != nil {
		return RunResult{}, err
	}
	results, err := RunCases(ctx, cases, opts)
	if err != nil {
		return RunResult{}, err
	}
	return WriteReports(results, opts)
}

func DefaultMinimumPassRate(mode Mode) float64 {
	if mode == ModeRouting {
		return 0.90
	}
	return 1
}
