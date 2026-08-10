package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/Thadopz/telltale/internal/watcher"
	"github.com/Thadopz/telltale/utility/storage"
)

// RunWatch is shared by telltale watch and watch_cmd. The watcher talks to
// collectors and SQLite directly; it does not call the HTTP adapter.
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
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		return runtimeError(err)
	}
	config, err := watcher.LoadConfig(ctx)
	if err != nil {
		return runtimeError(err)
	}
	if !config.Enabled {
		fmt.Fprintln(output, "watch is disabled; set watch.enabled=true in the active config")
		return nil
	}
	if err = storage.Initialize(ctx); err != nil {
		return runtimeError(err)
	}
	shutdown, err := observability.Init(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	}()

	runner := watcher.NewRunner(config, watcher.NewToolCollector(), watcher.NewSQLiteStore())
	if *runOnce {
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
		if runErr != nil {
			return runtimeError(runErr)
		}
		return nil
	}

	if err = runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return runtimeError(err)
	}
	return nil
}

func printWatchUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale watch [--once]")
}
