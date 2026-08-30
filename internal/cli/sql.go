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
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/sqlobserve"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/storage"
)

// RunSQL is shared by telltale sql and the sre-sql-observe compatibility
// binary. It never sends an internal HTTP request.
func RunSQL(ctx context.Context, args []string) error {
	configDir, commandArgs, err := splitConfigDir(args)
	if err != nil {
		return usageError(err)
	}
	return runSQL(ctx, commandArgs, configDir, os.Stdout)
}

func runSQL(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSQLUsage(os.Stderr)
		return nil
	}
	ctx = quietCommandContext(ctx)

	flags := flag.NewFlagSet("telltale sql "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("target", "default", "logical database target name; never a DSN")
	source := flags.String("source", "", "adapter-specific data source; leave empty for the configured adapter default")
	sortBy := flags.String("sort-by", "", "fixed sort mode for the selected query")
	limit := flags.Int("limit", 0, "maximum number of rows; defaults to the selected query's safe limit")
	maxBytes := flags.Int("max-bytes", 0, "maximum serialized result bytes; defaults to the CLI budget")
	since := flags.String("since", "", "inclusive RFC3339 start time; slow-sql only")
	until := flags.String("until", "", "exclusive RFC3339 end time; slow-sql only")
	last := flags.String("last", "", "relative history window such as 30m; slow-sql only")
	window := flags.String("window", "", "compatibility alias for --last")
	format := flags.String("format", "json", "json or table")
	jsonOutput := flags.Bool("json", false, "alias for --format json")
	sqlText := flags.String("sql", "", "raw-read SQL; only SELECT, SHOW, or EXPLAIN is allowed")
	if err := flags.Parse(args[1:]); err != nil {
		return usageError(err)
	}
	if *jsonOutput {
		*format = "json"
	}
	if *format != "json" && *format != "table" {
		return usageError(fmt.Errorf("unsupported format %q; use json or table", *format))
	}

	kind, err := queryKindForCommand(args[0])
	if err != nil {
		return usageError(err)
	}
	if kind == sqlobserve.QueryRawRead && strings.TrimSpace(*sqlText) == "" {
		return usageError(errors.New("--sql is required for raw-read"))
	}
	if kind != sqlobserve.QueryRawRead && strings.TrimSpace(*sqlText) != "" {
		return usageError(errors.New("--sql is only valid for raw-read"))
	}
	if strings.TrimSpace(*last) != "" && strings.TrimSpace(*window) != "" {
		return usageError(errors.New("--last and --window cannot be used together"))
	}
	timeWindow := strings.TrimSpace(*last)
	if timeWindow == "" {
		timeWindow = strings.TrimSpace(*window)
	}
	hasTimeParameters := strings.TrimSpace(*since) != "" || strings.TrimSpace(*until) != "" || timeWindow != ""
	if kind != sqlobserve.QuerySlowSQL && hasTimeParameters {
		return usageError(errors.New("--since, --until, --last, and --window are only valid for slow-sql"))
	}

	configure(ctx, configDir)
	if err := validateConfiguredSecurity(ctx); err != nil {
		return runtimeError(err)
	}
	if err := storage.Initialize(ctx); err != nil {
		return runtimeError(fmt.Errorf("initialize local observation history: %w", err))
	}
	if err := authorizeCLIRead(ctx, "db_slow_queries", authz.PermDatabaseRead); err != nil {
		return runtimeError(err)
	}
	observer, err := sqlobserve.NewConfiguredObserver(ctx)
	if err != nil {
		return runtimeError(err)
	}
	budget := observer.Budget
	requestedBytes := *maxBytes
	if requestedBytes <= 0 || requestedBytes > budget.CLIMaxBytes {
		requestedBytes = budget.CLIMaxBytes
	}
	defaultLimit := 20
	if kind == sqlobserve.QueryRawRead {
		defaultLimit = budget.HardMaxRows
	}
	requestedLimit := budget.CLILimit(*limit, defaultLimit)
	envelope, err := observer.Query(ctx, sqlobserve.QueryRequest{
		Kind:      kind,
		Target:    strings.TrimSpace(*target),
		Source:    strings.TrimSpace(*source),
		StartTime: strings.TrimSpace(*since),
		EndTime:   strings.TrimSpace(*until),
		Window:    timeWindow,
		SortBy:    strings.TrimSpace(*sortBy),
		Limit:     requestedLimit,
		MaxBytes:  requestedBytes,
		SQL:       *sqlText,
	})
	if err != nil {
		return runtimeError(err)
	}

	if _, ok := envelope.UsableEvidence(); ok {
		principal := authz.PrincipalFromContext(ctx)
		envelope = evidence.IssueEnvelope(envelope, evidence.IssueMetadata{
			Issuer:         "cli:telltale-sql",
			RunID:          evidence.NewRunID(),
			ConversationID: "cli",
			TenantID:       principal.TenantID,
			TrustLevel:     evidence.ContentTrustTrustedLocal,
			ExpiresAfter:   10 * time.Minute,
		})
	}
	if err := writeEnvelope(output, envelope, *format); err != nil {
		return runtimeError(err)
	}
	if envelope.Status != evidence.StatusSuccess {
		message := strings.TrimSpace(envelope.Summary)
		if message == "" {
			message = "SQL observation did not complete successfully"
		}
		return runtimeError(fmt.Errorf("%s: %s", envelope.Status, message))
	}
	return nil
}

func validateConfiguredSecurity(ctx context.Context) error {
	return appconfig.ValidateRuntimeSecurity(ctx)
}

func queryKindForCommand(command string) (sqlobserve.QueryKind, error) {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "server-health":
		return sqlobserve.QueryServerHealth, nil
	case "active-queries":
		return sqlobserve.QueryActiveQueries, nil
	case "wait-events":
		return sqlobserve.QueryWaitEvents, nil
	case "locks":
		return sqlobserve.QueryLocks, nil
	case "slow-sql":
		return sqlobserve.QuerySlowSQL, nil
	case "raw-read":
		return sqlobserve.QueryRawRead, nil
	default:
		return "", fmt.Errorf("unsupported SQL command %q", command)
	}
}

func writeEnvelope(output io.Writer, envelope evidence.Envelope, format string) error {
	if format == "table" {
		return writeTable(output, envelope)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func writeTable(output io.Writer, envelope evidence.Envelope) error {
	evidenceID := ""
	metadata := evidence.ResultMetadata{}
	if record, ok := envelope.UsableEvidence(); ok {
		evidenceID = record.EvidenceID
		metadata = record.ResultMetadata
	}
	if _, err := fmt.Fprintf(output, "status=%s data_status=%s evidence_id=%s\n", envelope.Status, envelope.DataStatus, evidenceID); err != nil {
		return err
	}
	if data, ok := envelope.Data.(map[string]any); ok {
		if _, err := fmt.Fprintf(output, "database_engine=%v target=%v capability=%v\n", data["database_engine"], data["target"], data["capability"]); err != nil {
			return err
		}
	}
	if metadata.ReturnedRows > 0 || metadata.Truncated {
		if _, err := fmt.Fprintf(output, "returned_rows=%d max_rows=%d returned_bytes=%d max_bytes=%d truncated=%t\n", metadata.ReturnedRows, metadata.MaxRows, metadata.ReturnedBytes, metadata.MaxBytes, metadata.Truncated); err != nil {
			return err
		}
	}
	if envelope.Summary != "" {
		if _, err := fmt.Fprintln(output, envelope.Summary); err != nil {
			return err
		}
	}
	if envelope.Data == nil {
		return nil
	}
	data, err := json.MarshalIndent(envelope.Data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(data))
	return err
}

func printSQLUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale sql <server-health|active-queries|wait-events|locks|slow-sql|raw-read> [options]")
	fmt.Fprintln(output, "The command uses the same bounded fixed-query observer as the database agent.")
	fmt.Fprintln(output, "slow-sql supports --since/--until or --last; --window is a compatibility alias for --last.")
	fmt.Fprintln(output, "Other commands are current snapshots and reject historical time flags.")
	fmt.Fprintln(output, "raw-read is for local human inspection and requires --sql.")
}
