package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Thadopz/telltale/internal/cli"
)

// Compatibility wrapper. The implementation lives in internal/cli.
func main() {
	if err := cli.RunSQL(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
