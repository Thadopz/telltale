package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/watcher"
	"github.com/Thadopz/telltale/utility/storage"
)

type incidentReader interface {
	ListIncidents(context.Context, watcher.IncidentQuery) ([]watcher.Incident, error)
	GetIncident(context.Context, string) (watcher.IncidentDetail, error)
}

func runIncident(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printIncidentUsage(os.Stderr)
		return nil
	}
	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		return runtimeError(err)
	}
	if err := authorizeCLIRead(ctx, "incident", authz.PermMetricsRead); err != nil {
		return runtimeError(err)
	}
	if err := storage.Initialize(ctx); err != nil {
		return runtimeError(err)
	}
	return runIncidentWithReader(ctx, args, output, watcher.NewSQLiteStore())
}

func runIncidentWithReader(ctx context.Context, args []string, output io.Writer, reader incidentReader) error {
	switch args[0] {
	case "list":
		return runIncidentList(ctx, args[1:], output, reader)
	case "show":
		return runIncidentShow(ctx, args[1:], output, reader)
	default:
		return usageError(fmt.Errorf("unsupported incident command %q; use list or show", args[0]))
	}
}

func runIncidentList(ctx context.Context, args []string, output io.Writer, reader incidentReader) error {
	flags := flag.NewFlagSet("telltale incident list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	status := flags.String("status", "", "firing or resolved")
	service := flags.String("service", "", "exact service name")
	limit := flags.Int("limit", 20, "maximum incidents to return; capped at 100")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 {
		return usageError(fmt.Errorf("unexpected argument %q", flags.Args()[0]))
	}
	if *status != "" && *status != watcher.StateFiring && *status != "resolved" {
		return usageError(fmt.Errorf("unsupported incident status %q", *status))
	}
	if *limit <= 0 || *limit > 100 {
		return usageError(fmt.Errorf("--limit must be between 1 and 100"))
	}
	if strings.TrimSpace(*service) != "" && !validIncidentSelector(strings.TrimSpace(*service)) {
		return usageError(fmt.Errorf("--service contains invalid characters"))
	}
	incidents, err := reader.ListIncidents(ctx, watcher.IncidentQuery{
		Status:  *status,
		Service: strings.TrimSpace(*service),
		Limit:   *limit,
	})
	if err != nil {
		return runtimeError(err)
	}
	if *jsonOutput {
		return writeIndentedJSON(output, incidents)
	}
	for _, incident := range incidents {
		fmt.Fprintf(output, "id=%s status=%s severity=%s service=%s rule=%s last_seen_at=%s diagnosis=%t\n",
			incident.ID,
			incident.Status,
			incident.Severity,
			incident.Service,
			incident.RuleID,
			incident.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			incident.Diagnosis != "",
		)
	}
	return nil
}

func runIncidentShow(ctx context.Context, args []string, output io.Writer, reader incidentReader) error {
	flags := flag.NewFlagSet("telltale incident show", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(moveBooleanFlagFirst(args, "--json")); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(flags.Args()[0]) == "" {
		return usageError(errors.New("incident id is required"))
	}
	incidentID := strings.TrimSpace(flags.Args()[0])
	if !validIncidentSelector(incidentID) {
		return usageError(errors.New("incident id contains invalid characters"))
	}
	detail, err := reader.GetIncident(ctx, incidentID)
	if errors.Is(err, sql.ErrNoRows) {
		return runtimeError(fmt.Errorf("incident %s not found", flags.Args()[0]))
	}
	if err != nil {
		return runtimeError(err)
	}
	if *jsonOutput {
		return writeIndentedJSON(output, detail)
	}
	incident := detail.Incident
	fmt.Fprintf(output, "id=%s status=%s severity=%s domain=%s service=%s environment=%s rule=%s\n",
		incident.ID,
		incident.Status,
		incident.Severity,
		incident.Domain,
		incident.Service,
		incident.Environment,
		incident.RuleID,
	)
	fmt.Fprintf(output, "first_seen_at=%s last_seen_at=%s latest_value=%v evidence_id=%s\n",
		incident.FirstSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		incident.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		incident.LatestValue,
		incident.EvidenceID,
	)
	if len(detail.Jobs) > 0 {
		job := detail.Jobs[0]
		fmt.Fprintf(output, "diagnosis_job=%s status=%s attempts=%d run_id=%s last_error=%s\n",
			job.ID,
			job.Status,
			job.AttemptCount,
			job.RunID,
			job.LastError,
		)
	}
	if incident.Diagnosis != "" {
		var result diagnosis.DiagnoseResult
		if err := json.Unmarshal([]byte(incident.Diagnosis), &result); err == nil && result.Answer != "" {
			fmt.Fprintln(output, result.Answer)
		}
	}
	return nil
}

func moveBooleanFlagFirst(args []string, name string) []string {
	ordered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			ordered = append(ordered, arg)
		}
	}
	for _, arg := range args {
		if arg != name && !strings.HasPrefix(arg, name+"=") {
			ordered = append(ordered, arg)
		}
	}
	return ordered
}

func validIncidentSelector(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("-_.:/@|", character) {
			continue
		}
		return false
	}
	return true
}

func writeIndentedJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return runtimeError(err)
	}
	return nil
}

func printIncidentUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale incident list [--status firing|resolved] [--service NAME] [--limit N] [--json]")
	fmt.Fprintln(output, "       telltale incident show <incident-id> [--json]")
}
