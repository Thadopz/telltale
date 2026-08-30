package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Thadopz/telltale/internal/authz"
	runtimepkg "github.com/Thadopz/telltale/internal/runtime"
	"github.com/Thadopz/telltale/utility/mem"
)

func runMemory(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Usage: telltale memory flush --conversation ID")
		return nil
	}
	if args[0] != "flush" {
		return usageError(fmt.Errorf("unknown memory command %q", args[0]))
	}
	flags := flag.NewFlagSet("telltale memory flush", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	conversationID := flags.String("conversation", "", "conversation to flush")
	if err := flags.Parse(args[1:]); err != nil {
		return usageError(err)
	}
	if *conversationID == "" {
		return usageError(fmt.Errorf("--conversation is required"))
	}

	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	runtime, err := runtimepkg.NewRuntime(ctx)
	if err != nil {
		return runtimeError(err)
	}
	defer func() { _ = runtimepkg.CloseWithTimeout(runtime) }()
	principal := authz.ConfiguredPrincipal(ctx)
	if err = runtime.Memory.FlushConversation(ctx, mem.MemoryPrincipal{
		TenantID: principal.TenantID,
		UserID:   principal.UserID,
	}, *conversationID); err != nil {
		return runtimeError(err)
	}
	fmt.Fprintf(output, "memory_flushed conversation=%s\n", *conversationID)
	return nil
}
