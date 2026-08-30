package watcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestCardServiceAlertOpensIncidentAndRunsAutomaticDiagnosis(t *testing.T) {
	prepareWatcherDatabase(t)
	prometheus := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{
			"status":"success",
			"data":{"alerts":[
				{"labels":{"alertname":"CardErrors","service":"card-service"},"annotations":{"description":"card failures"},"state":"firing","activeAt":"2026-08-10T10:00:00Z"},
				{"labels":{"alertname":"OrderErrors","service":"order-service"},"annotations":{"description":"order failures"},"state":"firing","activeAt":"2026-08-10T10:00:00Z"}
			]}
		}`)
	}))
	defer prometheus.Close()

	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("create config adapter: %v", err)
	}
	adapter.SetContent("tools:\n  prometheus:\n    base_url: \"" + prometheus.URL + "\"\n")
	previousAdapter := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	defer g.Cfg().SetAdapter(previousAdapter)

	rule := Rule{
		ID:              "card-service-active-alerts",
		Domain:          "observability",
		Collector:       CollectorPrometheusAlerts,
		Service:         "card-service",
		WindowMinutes:   5,
		ValuePath:       "alerts",
		Operator:        OperatorCountAbove,
		Threshold:       0,
		Severity:        "critical",
		FiringWindows:   2,
		RecoveryWindows: 2,
		AutoDiagnose:    true,
		MatchLabels:     map[string]string{"service": "card-service"},
		AlertStates:     []string{"firing"},
	}
	config := Config{
		Enabled:     true,
		TenantID:    "tenant-a",
		Environment: "prod",
		Diagnosis: DiagnosisConfig{
			Enabled:             true,
			PollIntervalSeconds: 2,
			RunTimeoutSeconds:   300,
			RetryDelaysSeconds:  []int{30, 120, 600},
		},
		Rules: []Rule{rule},
	}
	store := NewSQLiteStore()
	runner := NewRunner(config, NewToolCollector(), store)
	current := time.Date(2026, 8, 10, 10, 1, 0, 0, time.UTC)
	runner.now = func() time.Time { return current }

	for cycle := 0; cycle < 2; cycle++ {
		if _, err := runner.RunOnce(context.Background()); err != nil {
			t.Fatalf("watch cycle %d: %v", cycle+1, err)
		}
		current = current.Add(time.Minute)
	}
	incidents, err := store.ListIncidents(context.Background(), IncidentQuery{Service: "card-service"})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("expected one card-service incident: incidents=%#v err=%v", incidents, err)
	}

	service := &workerDiagnosticService{result: diagnosis.DiagnoseResult{Answer: "verified card-service alert", RunID: "run-card"}}
	worker := NewDiagnosisWorker(config, store, service)
	worker.now = func() time.Time { return current }
	processed, err := worker.RunReady(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("process automatic diagnosis: processed=%d err=%v", processed, err)
	}
	detail, err := store.GetIncident(context.Background(), incidents[0].ID)
	if err != nil {
		t.Fatalf("read diagnosed incident: %v", err)
	}
	if len(detail.Jobs) != 1 || detail.Jobs[0].Status != DiagnosisJobSucceeded || detail.Incident.Diagnosis == "" {
		t.Fatalf("automatic diagnosis did not complete: %#v", detail)
	}
	if service.request.Service != "card-service" || service.request.ObservedValue != 1 {
		t.Fatalf("unexpected automatic diagnosis scope: %#v", service.request)
	}
}
