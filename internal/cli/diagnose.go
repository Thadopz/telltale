package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Thadopz/telltale/internal/diagnosis"
	runtimepkg "github.com/Thadopz/telltale/internal/runtime"
)

type diagnoseOptions struct {
	question       string
	conversationID string
	jsonOutput     bool
}

func runDiagnose(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		printDiagnoseUsage(os.Stderr)
		return nil
	}
	options, err := parseDiagnoseArgs(args)
	if err != nil {
		return diagnoseError(output, hasJSONFlag(args), usageError(err))
	}
	if options.question == "" {
		data, readErr := io.ReadAll(os.Stdin)
		if readErr != nil {
			return runtimeError(fmt.Errorf("read question from stdin: %w", readErr))
		}
		options.question = strings.TrimSpace(string(data))
	}
	if options.question == "" {
		return diagnoseError(output, options.jsonOutput, usageError(fmt.Errorf("question is required")))
	}

	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	rt, err := runtimepkg.NewRuntime(ctx)
	if err != nil {
		return diagnoseError(output, options.jsonOutput, runtimeError(err))
	}
	resultErr := runDiagnoseWithOptions(ctx, options, rt.Diagnostic, output)
	closeErr := rt.Close(ctx)
	if resultErr != nil {
		return resultErr
	}
	if closeErr != nil {
		return runtimeError(fmt.Errorf("close runtime: %w", closeErr))
	}
	return nil
}

// RunDiagnoseWithService is a small injection point for deterministic tests
// and embedded callers. Production CLI commands use the bootstrapped runtime.
func RunDiagnoseWithService(ctx context.Context, args []string, service diagnosis.DiagnosticService, output io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		printDiagnoseUsage(os.Stderr)
		return nil
	}
	options, err := parseDiagnoseArgs(args)
	if err != nil {
		return diagnoseError(output, hasJSONFlag(args), usageError(err))
	}
	if options.question == "" {
		return diagnoseError(output, options.jsonOutput, usageError(fmt.Errorf("question is required")))
	}
	return runDiagnoseWithOptions(ctx, options, service, output)
}

func runDiagnoseWithOptions(ctx context.Context, options diagnoseOptions, service diagnosis.DiagnosticService, output io.Writer) error {
	if service == nil {
		return diagnoseError(output, options.jsonOutput, runtimeError(fmt.Errorf("diagnostic service is unavailable")))
	}
	result, err := service.Diagnose(ctx, diagnosis.DiagnoseRequest{
		Question:       options.question,
		ConversationID: options.conversationID,
	})
	if err != nil {
		return diagnoseError(output, options.jsonOutput, runtimeError(err))
	}
	if err := writeDiagnoseResult(output, result, options.jsonOutput); err != nil {
		return runtimeError(err)
	}
	return nil
}

func parseDiagnoseArgs(args []string) (diagnoseOptions, error) {
	flags := flag.NewFlagSet("telltale diagnose", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	conversationID := flags.String("conversation-id", "", "resume this SQLite conversation explicitly")
	jsonOutput := flags.Bool("json", false, "write the structured diagnosis result as JSON")
	if err := flags.Parse(reorderDiagnoseFlags(args)); err != nil {
		return diagnoseOptions{}, err
	}
	question := strings.TrimSpace(strings.Join(flags.Args(), " "))
	return diagnoseOptions{
		question:       question,
		conversationID: strings.TrimSpace(*conversationID),
		jsonOutput:     *jsonOutput,
	}, nil
}

func reorderDiagnoseFlags(args []string) []string {
	flagArgs := make([]string, 0, len(args))
	questionArgs := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json", arg == "--json=true", arg == "--json=false":
			flagArgs = append(flagArgs, arg)
		case arg == "--conversation-id":
			flagArgs = append(flagArgs, arg)
			if index+1 < len(args) {
				index++
				flagArgs = append(flagArgs, args[index])
			}
		case strings.HasPrefix(arg, "--conversation-id="):
			flagArgs = append(flagArgs, arg)
		case strings.HasPrefix(arg, "-"):
			flagArgs = append(flagArgs, arg)
		default:
			questionArgs = append(questionArgs, arg)
		}
	}
	return append(flagArgs, questionArgs...)
}

func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}

func writeDiagnoseResult(output io.Writer, result diagnosis.DiagnoseResult, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	_, err := fmt.Fprintln(output, result.Answer)
	return err
}

func diagnoseError(output io.Writer, jsonOutput bool, err error) error {
	if !jsonOutput {
		return err
	}
	writeJSONError(output, err)
	return &Error{Code: ExitCode(err), Err: err, Reported: true}
}

func printDiagnoseUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: telltale diagnose [--json] [--conversation-id ID] <question>")
	fmt.Fprintln(w, "       telltale diagnose [options] < question.txt")
	fmt.Fprintln(w, "The command runs one read-only diagnostic through the local core runtime.")
}
