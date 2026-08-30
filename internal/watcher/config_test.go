package watcher

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

func TestPrometheusWatchRuleAcceptsExactServiceAndStateFilters(t *testing.T) {
	rule := Rule{
		ID:              "card-service-alerts",
		Domain:          "observability",
		Collector:       CollectorPrometheusAlerts,
		Service:         "card-service",
		ValuePath:       "alerts",
		Operator:        OperatorCountAbove,
		Severity:        "critical",
		MatchLabels:     map[string]string{"service": "card-service"},
		AlertStates:     []string{"firing"},
		FiringWindows:   2,
		RecoveryWindows: 2,
	}
	if err := validateRules([]Rule{rule}); err != nil {
		t.Fatalf("validate card service rule: %v", err)
	}
}

func TestDatabaseProbeRequiresConfiguredLogicalTarget(t *testing.T) {
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("create config adapter: %v", err)
	}
	adapter.SetContent(`database:
  targets:
    card_service_primary:
      type: postgresql
`)
	previous := g.Cfg().GetAdapter()
	g.Cfg().SetAdapter(adapter)
	defer g.Cfg().SetAdapter(previous)

	rule := Rule{ID: "card-alert", Domain: "observability", DatabaseProbe: DatabaseProbe{Enabled: true, Target: "card_service_primary"}}
	if err := validateDatabaseProbes(context.Background(), []Rule{rule}); err != nil {
		t.Fatalf("validate configured database probe: %v", err)
	}
	rule.DatabaseProbe.Target = "missing_target"
	if err := validateDatabaseProbes(context.Background(), []Rule{rule}); err == nil {
		t.Fatal("expected unknown database target to be rejected")
	}
	rule.Domain = "payment"
	if err := validateDatabaseProbes(context.Background(), []Rule{rule}); err == nil {
		t.Fatal("expected non-observability database probe to be rejected")
	}
}

func TestPrometheusWatchRuleRejectsUnsafeLabelValues(t *testing.T) {
	rule := Rule{
		ID:          "card-service-alerts",
		Domain:      "observability",
		Collector:   CollectorPrometheusAlerts,
		ValuePath:   "alerts",
		Operator:    OperatorCountAbove,
		MatchLabels: map[string]string{"service": "card-service\nforged"},
	}
	if err := validateRules([]Rule{rule}); err == nil {
		t.Fatal("expected unsafe label value to be rejected")
	}
	rule.MatchLabels = map[string]string{"service": strings.Repeat("x", maxLabelValueRunes+1)}
	if err := validateRules([]Rule{rule}); err == nil {
		t.Fatal("expected oversized label value to be rejected")
	}
}

func TestDiagnosisConfigUsesPlannedDefaults(t *testing.T) {
	config := Config{}
	applyConfigDefaults(&config)
	if config.DiagnosisPollInterval() != 2*time.Second {
		t.Fatalf("unexpected poll interval: %s", config.DiagnosisPollInterval())
	}
	if config.DiagnosisRunTimeout() != 5*time.Minute {
		t.Fatalf("unexpected run timeout: %s", config.DiagnosisRunTimeout())
	}
	delays := config.DiagnosisRetryDelays()
	if len(delays) != 3 || delays[0] != 30*time.Second || delays[1] != 2*time.Minute || delays[2] != 10*time.Minute {
		t.Fatalf("unexpected retry delays: %#v", delays)
	}
}
