package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/watcher"
)

type fakeIncidentReader struct {
	query  watcher.IncidentQuery
	items  []watcher.Incident
	detail watcher.IncidentDetail
	err    error
}

func (reader *fakeIncidentReader) ListIncidents(_ context.Context, query watcher.IncidentQuery) ([]watcher.Incident, error) {
	reader.query = query
	return reader.items, reader.err
}

func (reader *fakeIncidentReader) GetIncident(context.Context, string) (watcher.IncidentDetail, error) {
	return reader.detail, reader.err
}

func TestIncidentListUsesFiltersAndWritesHumanOutput(t *testing.T) {
	reader := &fakeIncidentReader{items: []watcher.Incident{{
		ID:         "inc_card",
		Status:     watcher.StateFiring,
		Severity:   "critical",
		Service:    "card-service",
		RuleID:     "card-service-alerts",
		LastSeenAt: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
		Diagnosis:  `{"answer":"checked"}`,
	}}}
	var output bytes.Buffer
	err := runIncidentWithReader(context.Background(), []string{"list", "--status", "firing", "--service", "card-service", "--limit", "5"}, &output, reader)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if reader.query.Status != "firing" || reader.query.Service != "card-service" || reader.query.Limit != 5 {
		t.Fatalf("unexpected query: %#v", reader.query)
	}
	if !strings.Contains(output.String(), "inc_card") || !strings.Contains(output.String(), "diagnosis=true") {
		t.Fatalf("unexpected human output: %s", output.String())
	}
}

func TestIncidentShowWritesStructuredJSON(t *testing.T) {
	resultJSON, err := json.Marshal(diagnosis.DiagnoseResult{Answer: "verified card-service alert", RunID: "run-1"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	reader := &fakeIncidentReader{detail: watcher.IncidentDetail{
		Incident: watcher.Incident{ID: "inc_card", Service: "card-service", Diagnosis: string(resultJSON)},
		Jobs:     []watcher.DiagnosisJob{{ID: "diag_card", Status: watcher.DiagnosisJobSucceeded, AttemptCount: 1}},
	}}
	var output bytes.Buffer
	if err := runIncidentWithReader(context.Background(), []string{"show", "inc_card", "--json"}, &output, reader); err != nil {
		t.Fatalf("show incident: %v", err)
	}
	if !strings.Contains(output.String(), `"id": "inc_card"`) || !strings.Contains(output.String(), `"status": "succeeded"`) {
		t.Fatalf("unexpected JSON output: %s", output.String())
	}
}

func TestIncidentShowReturnsNotFound(t *testing.T) {
	reader := &fakeIncidentReader{err: sql.ErrNoRows}
	err := runIncidentWithReader(context.Background(), []string{"show", "missing"}, &bytes.Buffer{}, reader)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestIncidentCommandsRejectControlCharacters(t *testing.T) {
	reader := &fakeIncidentReader{}
	err := runIncidentWithReader(context.Background(), []string{"show", "inc_good\nforged"}, &bytes.Buffer{}, reader)
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Fatalf("expected invalid incident id error, got %v", err)
	}
	err = runIncidentWithReader(context.Background(), []string{"list", "--service", "card-service\r\nforged"}, &bytes.Buffer{}, reader)
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Fatalf("expected invalid service error, got %v", err)
	}
}

func TestIncidentReadRequiresMetricsPermission(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "viewer", Roles: []string{"viewer"}})
	if err := authorizeCLIRead(ctx, "incident", authz.PermMetricsRead); err == nil {
		t.Fatal("expected viewer without metrics permission to be denied")
	}
}
