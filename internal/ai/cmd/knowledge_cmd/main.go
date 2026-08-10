package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Thadopz/telltale/internal/cli"
)

// Compatibility wrapper. The implementation lives in internal/cli.
func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"index"}
	}
	if err := cli.RunKnowledge(context.Background(), args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
