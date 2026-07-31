package main

import (
	"SREagent/internal/appconfig"
	"SREagent/internal/observability"
	"SREagent/internal/watcher"
	"SREagent/utility/client"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	"github.com/gogf/gf/v2/frame/g"
)

func main() {
	runOnce := flag.Bool("once", false, "evaluate every configured watch rule once and exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appconfig.Configure(ctx)

	config, err := watcher.LoadConfig(ctx)
	if err != nil {
		exitWithError(err)
	}
	if !config.Enabled {
		fmt.Println("watch is disabled; set watch.enabled=true in the active config")
		return
	}
	if err = client.InitMySQL(ctx); err != nil {
		exitWithError(err)
	}

	shutdownObservability, err := observability.Init(ctx)
	if err != nil {
		exitWithError(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdownObservability(shutdownCtx); shutdownErr != nil {
			g.Log().Errorf(ctx, "shutdown OpenTelemetry: %v", shutdownErr)
		}
	}()

	runner := watcher.NewRunner(config, watcher.NewToolCollector(), watcher.NewMySQLStore())
	if *runOnce {
		results, runErr := runner.RunOnce(ctx)
		for _, result := range results {
			fmt.Printf(
				"rule=%s state=%s abnormal=%t transition=%s value=%v evidence_id=%s error=%v\n",
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
			exitWithError(runErr)
		}
		return
	}

	err = runner.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		exitWithError(err)
	}
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
