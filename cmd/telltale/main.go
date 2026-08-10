package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Thadopz/telltale/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.Run(ctx, os.Args[1:])
	if err == nil {
		return
	}
	if !cli.IsReported(err) {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(cli.ExitCode(err))
}
