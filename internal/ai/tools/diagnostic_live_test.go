package tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/tool"
)

func TestDiagnosticLiveMetricCycle(t *testing.T) {
	endpoint := os.Getenv("TELLTALE_RCAEVAL_DISK_MCP_URL")
	if endpoint == "" {
		t.Skip("set TELLTALE_RCAEVAL_DISK_MCP_URL to the real frozen disk case server")
	}
	ctx := runstate.WithState(authz.WithPrincipal(t.Context(), authz.Principal{UserID: "diagnostic-live", TenantID: "test"}))
	runstate.EnableDiagnostic(ctx)
	remote, err := loadStreamableHTTPMCPTools(ctx, endpoint, nil, "diagnostic-live")
	if err != nil {
		t.Fatal(err)
	}
	available := make(map[string]tool.InvokableTool)
	for _, candidate := range remote {
		info, err := candidate.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		available[info.Name] = candidate.(tool.InvokableTool)
	}
	runstate.RegisterDiagnosticTools(ctx, []string{"get_metric"}, "get_metric")
	read := func(name, arguments string) {
		t.Helper()
		raw, err := available[name].InvokableRun(ctx, arguments)
		if err != nil {
			t.Fatal(err)
		}
		payload, ok := configuredMCPTextOutput(raw)
		if !ok {
			t.Fatal("real tool returned no JSON text")
		}
		signed := IssueToolOutput(ctx, name, payload)
		env := evidence.Parse(signed).Envelope
		if env.Status != evidence.StatusSuccess {
			t.Fatalf("real signed observation rejected: %s", signed)
		}
		runstate.RecordDiagnostic(ctx, name, arguments, env)
	}
	read("rank_anomalies", `{"modality":"metrics","top_k":4}`)
	seed := runstate.DiagnosticSnapshot(ctx)
	var fact runstate.Fact
	for _, value := range seed.Facts {
		if value.Metric == "emailservice_diskio" {
			fact = value
		}
	}
	if fact.EvidenceID == "" || fact.Before != 0 || fact.After <= 0 {
		t.Fatalf("constant-baseline disk signal lost: %#v", seed)
	}
	if runstate.DiagnosticQueryAllowed(ctx, "get_metric", `{"name":"emailservice_diskio"}`) {
		t.Fatal("unplanned query was allowed after obtaining seed evidence")
	}
	check := func(metric, direction string) runstate.Check {
		return runstate.Check{
			Tool: "get_metric", Arguments: map[string]any{"name": metric},
			Metric: metric, Direction: direction,
			Discriminates: "Distinguish disk activity from CPU-only pressure",
		}
	}
	proposals := []runstate.Candidate{
		{ID: "disk", Statement: "Disk pressure may explain the service resource increase", Service: fact.Service, Start: fact.Start, End: fact.End, EvidenceIDs: []string{fact.EvidenceID}, Checks: []runstate.Check{check("emailservice_diskio", "increased")}},
		{ID: "cpu_only", Statement: "CPU pressure without increased disk activity", Service: fact.Service, Start: fact.Start, End: fact.End, EvidenceIDs: []string{fact.EvidenceID}, Checks: []runstate.Check{check("emailservice_cpu", "increased"), check("emailservice_diskio", "unchanged")}},
	}
	if err := runstate.ProposeDiagnostic(ctx, proposals); err != nil {
		t.Fatal(err)
	}
	if !runstate.DiagnosticQueryAllowed(ctx, "get_metric", `{"name":"emailservice_diskio"}`) || runstate.DiagnosticQueryAllowed(ctx, "get_metric", `{"name":"redis_diskio"}`) {
		t.Fatal("query policy did not enforce the declared discriminating checks")
	}
	start, err := time.Parse(time.RFC3339, fact.Start)
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.RFC3339, fact.End)
	if err != nil {
		t.Fatal(err)
	}
	parameters := map[string]any{"name": "emailservice_diskio", "points": 20, "start": start.Unix(), "end": end.Unix()}
	equivalent, _ := json.Marshal(parameters)
	if !runstate.DiagnosticQueryAllowed(ctx, "get_metric", string(equivalent)) {
		t.Fatal("equivalent display parameters and pinned default window were rejected")
	}
	parameters["start"] = start.Unix() - 1
	different, _ := json.Marshal(parameters)
	if runstate.DiagnosticQueryAllowed(ctx, "get_metric", string(different)) {
		t.Fatal("changed query scope was accepted")
	}
	if runstate.DiagnosticReady(ctx) {
		t.Fatal("ranking alone completed the discriminating checks")
	}
	read("get_metric", `{"name":"emailservice_diskio"}`)
	read("get_metric", `{"name":"emailservice_cpu"}`)
	result := runstate.DiagnosticSnapshot(ctx)
	if result.Candidates[0].Status != "supported" || result.Candidates[1].Status != "refuted" || !runstate.DiagnosticReady(ctx) {
		t.Fatalf("real evidence did not update competing predictions: %#v", result)
	}
	if !strings.Contains(result.Conclusion, "causality_unconfirmed") || len(result.Transitions) == 0 {
		t.Fatal("prediction support was confused with verified cause, or transitions were lost")
	}
	encoded, _ := json.Marshal(result)
	if err := os.WriteFile(os.Getenv("TELLTALE_DIAGNOSTIC_AUDIT"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	proposals[0].EvidenceIDs = []string{"ev_unknown"}
	if err := runstate.ProposeDiagnostic(ctx, proposals); err == nil {
		t.Fatal("unknown evidence was accepted")
	}
	proposals[0].EvidenceIDs = []string{fact.EvidenceID}
	proposals[0].Service = "unobserved_service"
	if err := runstate.ProposeDiagnostic(ctx, proposals); err == nil {
		t.Fatal("cross-service hypothesis was accepted")
	}
	if runstate.DiagnosticSnapshot(ctx).Candidates[0].Service != fact.Service {
		t.Fatal("rejected caller changes mutated validated state")
	}
}
