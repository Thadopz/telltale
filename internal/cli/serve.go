package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Thadopz/telltale/internal/runtime"
	"github.com/Thadopz/telltale/internal/server"
)

func runServe(ctx context.Context, args []string, configDir string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, "Usage: telltale serve [--host HOST] [--port PORT]")
		return nil
	}
	flags := flag.NewFlagSet("telltale serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	host := flags.String("host", "", "HTTP listen host; defaults to server.address")
	port := flags.Int("port", 0, "HTTP listen port; defaults to server.address")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 {
		return usageError(fmt.Errorf("unexpected argument %q", flags.Args()[0]))
	}
	if *port < 0 || *port > 65535 {
		return usageError(fmt.Errorf("--port must be between 1 and 65535"))
	}

	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	rt, err := runtime.NewRuntime(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer func() {
		_ = runtime.CloseWithTimeout(rt)
	}()
	if err := server.Run(ctx, rt, server.Options{Host: *host, Port: *port}); err != nil {
		return runtimeError(err)
	}
	return nil
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale <command> [options]")
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  diagnose <question>       run one read-only backend diagnosis")
	fmt.Fprintln(output, "  serve                     start the optional HTTP adapter")
	fmt.Fprintln(output, "  sql <query>               inspect bounded SQL observations")
	fmt.Fprintln(output, "  code search               search the controlled code snapshot")
	fmt.Fprintln(output, "  watch [--once]            run anomaly watcher")
	fmt.Fprintln(output, "  knowledge index|recall    manage local knowledge")
	fmt.Fprintln(output, "Global option: --config-dir PATH")
}

// Run dispatches the canonical telltale command. It deliberately returns an
// error instead of exiting so tests and compatibility wrappers can reuse it.
func Run(ctx context.Context, args []string) error {
	configDir, commandArgs, err := splitConfigDir(args)
	if err != nil {
		return usageError(err)
	}
	if len(commandArgs) == 0 || commandArgs[0] == "-h" || commandArgs[0] == "--help" {
		printUsage(os.Stderr)
		return nil
	}

	switch commandArgs[0] {
	case "diagnose":
		return runDiagnose(ctx, commandArgs[1:], configDir, os.Stdout)
	case "serve":
		return runServe(ctx, commandArgs[1:], configDir)
	case "sql":
		return runSQL(ctx, commandArgs[1:], configDir, os.Stdout)
	case "code":
		return runCode(ctx, commandArgs[1:], configDir, os.Stdout)
	case "watch":
		return runWatch(ctx, commandArgs[1:], configDir, os.Stdout)
	case "knowledge":
		return runKnowledge(ctx, commandArgs[1:], configDir, os.Stdout)
	default:
		return usageError(fmt.Errorf("unknown command %q", commandArgs[0]))
	}
}
