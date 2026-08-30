package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
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
	if opts.Mode == ModeEvidenceAblation {
		if err := validateEvidenceAblationEnvironment(ctx); err != nil {
			return RunResult{}, err
		}
	}
	cases, err := LoadCases(opts.CasesPath)
	if err != nil {
		return RunResult{}, err
	}
	cases, err = filterCasesByID(cases, opts.CaseIDs)
	if err != nil {
		return RunResult{}, err
	}
	results, err := RunCases(ctx, cases, opts)
	if err != nil {
		return RunResult{}, err
	}
	return WriteReports(results, opts)
}

func filterCasesByID(cases []Case, requested []string) ([]Case, error) {
	if len(requested) == 0 {
		return cases, nil
	}
	wanted := make(map[string]bool, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = true
		}
	}
	filtered := make([]Case, 0, len(wanted))
	for _, evalCase := range cases {
		if wanted[evalCase.ID] {
			filtered = append(filtered, evalCase)
			delete(wanted, evalCase.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown eval case ids: %s", strings.Join(missing, ", "))
	}
	return filtered, nil
}

func DefaultMinimumPassRate(mode Mode) float64 {
	if mode == ModeEvidenceAblation {
		return 0
	}
	if mode == ModeRouting {
		return 0.90
	}
	return 1
}

func validateEvidenceAblationEnvironment(ctx context.Context) error {
	environment := "standalone"
	value, err := g.Cfg().Get(ctx, "runtime.environment")
	if err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		environment = strings.ToLower(strings.TrimSpace(value.String()))
	}
	if environment != "eval" && environment != "test" {
		return fmt.Errorf("evidence_ablation is eval-only; runtime.environment must be eval or test")
	}
	return nil
}
