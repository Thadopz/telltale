package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Thadopz/telltale/internal/ai/sqlobserve"
	runtimepkg "github.com/Thadopz/telltale/internal/runtime"
	"github.com/Thadopz/telltale/internal/watcher"
)

// RunWatch is shared by telltale watch and watch_cmd. It reuses the local
// runtime and never calls the optional HTTP adapter.
func RunWatch(ctx context.Context, args []string) error {
	configDir, commandArgs, err := splitConfigDir(args)
	if err != nil {
		return usageError(err)
	}
	return runWatch(ctx, commandArgs, configDir, os.Stdout)
}

func runWatch(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		printWatchUsage(os.Stderr)
		return nil
	}
	flags := flag.NewFlagSet("telltale watch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runOnce := flags.Bool("once", false, "evaluate every configured watch rule once and exit")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 {
		return usageError(fmt.Errorf("unexpected argument %q", flags.Args()[0]))
	}

	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	config, err := watcher.LoadConfig(ctx)
	if err != nil {
		return runtimeError(err)
	}
	if !config.Enabled {
		fmt.Fprintln(output, "watch is disabled; set watch.enabled=true in the active config")
		return nil
	}
	rt, err := runtimepkg.NewRuntime(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer func() { _ = runtimepkg.CloseWithTimeout(rt) }()

	store := watcher.NewSQLiteStore()
	runner := watcher.NewRunner(config, watcher.NewToolCollector(), store)
	worker := watcher.NewDiagnosisWorker(config, store, rt.Diagnostic)
	historySampler, err := sqlobserve.NewConfiguredHistorySampler(ctx)
	if err != nil {
		return runtimeError(err)
	}
	if *runOnce {
		if rt.Memory != nil {
			processed, memoryErr := rt.Memory.RunReady(ctx)
			fmt.Fprintf(output, "memory_buffers_processed=%d error=%v\n", processed, memoryErr)
			if memoryErr != nil {
				return runtimeError(memoryErr)
			}
		}
		if historySampler.Enabled() {
			sampled, sampleErr := historySampler.RunOnce(ctx)
			fmt.Fprintf(output, "database_history_targets_sampled=%d error=%v\n", sampled, sampleErr)
			if sampleErr != nil {
				return runtimeError(sampleErr)
			}
		}
		results, runErr := runner.RunOnce(ctx)
		for _, result := range results {
			fmt.Fprintf(output, "rule=%s state=%s abnormal=%t transition=%s value=%v evidence_id=%s error=%v\n",
				result.Rule.ID,
				result.State.State,
				result.Abnormal,
				result.Transition,
				result.Snapshot.Value,
				result.Snapshot.EvidenceID,
				result.Err,
			)
		}
		if config.Diagnosis.Enabled {
			processed, workerErr := worker.RunReady(ctx)
			fmt.Fprintf(output, "diagnosis_jobs_processed=%d error=%v\n", processed, workerErr)
			if workerErr != nil && runErr == nil {
				runErr = workerErr
			}
		}
		if runErr != nil {
			return runtimeError(runErr)
		}
		return nil
	}

	watchContext, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	var memoryDone chan error
	if rt.Memory != nil {
		memoryDone = make(chan error, 1)
		go func() {
			memoryDone <- rt.Memory.Run(watchContext)
		}()
	}
	var workerDone chan error
	if config.Diagnosis.Enabled {
		workerDone = make(chan error, 1)
		go func() {
			workerDone <- worker.Run(watchContext)
		}()
	}
	var samplerDone chan error
	if historySampler.Enabled() {
		samplerDone = make(chan error, 1)
		go func() {
			samplerDone <- historySampler.Run(watchContext)
		}()
	}
	err = runner.Run(watchContext)
	stopWatch()
	if memoryDone != nil {
		memoryErr := <-memoryDone
		if memoryErr != nil && !errors.Is(memoryErr, context.Canceled) && err == nil {
			err = memoryErr
		}
	}
	if workerDone != nil {
		workerErr := <-workerDone
		if workerErr != nil && !errors.Is(workerErr, context.Canceled) && err == nil {
			err = workerErr
		}
	}
	if samplerDone != nil {
		samplerErr := <-samplerDone
		if samplerErr != nil && !errors.Is(samplerErr, context.Canceled) && err == nil {
			err = samplerErr
		}
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return runtimeError(err)
	}
	return nil
}

func printWatchUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale watch [--once]")
}
