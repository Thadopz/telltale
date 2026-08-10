package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Thadopz/telltale/internal/cli"
)

// Compatibility wrapper for the historical recall command.
func main() {
	if err := cli.RunKnowledge(context.Background(), append([]string{"recall"}, os.Args[1:]...)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
