package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/codeintel"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
)

type codeCommandOutput struct {
	Search codeintel.SearchResult `json:"search"`
	Read   *codeintel.ReadResult  `json:"read,omitempty"`
}

// RunCode is shared by telltale code and the sre-code-search compatibility
// binary. Handles are read only in the same process that created them.
func RunCode(ctx context.Context, args []string) error {
	configDir, commandArgs, err := splitConfigDir(args)
	if err != nil {
		return usageError(err)
	}
	return runCode(ctx, commandArgs, configDir, os.Stdout)
}

func runCode(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printCodeUsage(os.Stderr)
		return nil
	}
	if args[0] != "search" {
		return usageError(fmt.Errorf("unsupported command %q; use search", args[0]))
	}
	ctx = quietCommandContext(ctx)

	flags := flag.NewFlagSet("telltale code search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	query := flags.String("query", "", "exact code anchor, symbol, route, SQL digest, or config key")
	mode := flags.String("mode", string(codeintel.SearchExact), "exact, symbol, reference, or config")
	pathScope := flags.String("scope", string(codeintel.ScopeBusiness), "business, api, config, or migration")
	limit := flags.Int("limit", 0, "maximum number of results")
	readFirst := flags.Bool("read-first", false, "read the first bounded hit in the same process")
	readMode := flags.String("read-mode", "match_context", "match_context or enclosing_symbol")
	if err := flags.Parse(args[1:]); err != nil {
		return usageError(err)
	}
	if strings.TrimSpace(*query) == "" {
		return usageError(errors.New("--query is required"))
	}

	configure(ctx, configDir)
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		return runtimeError(err)
	}
	if err := authorizeCLIRead(ctx, "code_search", authz.PermCodeRead); err != nil {
		return runtimeError(err)
	}
	service, err := codeintel.LoadFromConfig(ctx)
	if err != nil {
		return runtimeError(err)
	}
	if service == nil {
		return runtimeError(errors.New("code search is disabled; enable code.enabled and configure the card_service snapshot"))
	}
	result, err := service.Search(ctx, codeintel.SearchRequest{
		Query:     *query,
		Mode:      codeintel.SearchMode(*mode),
		PathScope: codeintel.PathScope(*pathScope),
		Limit:     *limit,
	})
	if err != nil {
		return runtimeError(err)
	}
	outputValue := codeCommandOutput{Search: result}
	if *readFirst && len(result.Hits) > 0 {
		read, readErr := service.Read(ctx, codeintel.ReadRequest{Handle: result.Hits[0].Handle, Mode: *readMode})
		if readErr != nil {
			return runtimeError(readErr)
		}
		outputValue.Read = &read
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(outputValue); err != nil {
		return runtimeError(err)
	}
	return nil
}

func printCodeUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale code search --query <anchor> [options]")
	fmt.Fprintln(output, "The command uses the immutable, sanitized card_service snapshot.")
}
