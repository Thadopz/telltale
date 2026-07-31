package watcher

import "testing"

func TestSnapshotValueReadsNestedNumericPath(t *testing.T) {
	data := map[string]any{
		"data": map[string]any{
			"successRate": "0.975",
			"total":       float64(240),
		},
	}
	rule := Rule{
		ValuePath:       "data.successRate",
		SampleCountPath: "data.total",
		Operator:        OperatorLessThan,
	}

	value, err := snapshotValue(data, rule)
	if err != nil || value != 0.975 {
		t.Fatalf("unexpected value=%v err=%v", value, err)
	}
	samples, err := snapshotSampleCount(data, rule)
	if err != nil || samples != 240 {
		t.Fatalf("unexpected samples=%d err=%v", samples, err)
	}
}

func TestSnapshotValueCountsPrometheusAlerts(t *testing.T) {
	data := map[string]any{
		"alerts": []any{
			map[string]any{"alert_name": "HighErrorRate"},
			map[string]any{"alert_name": "HighLatency"},
		},
	}
	value, err := snapshotValue(data, Rule{Operator: OperatorCountAbove})
	if err != nil || value != 2 {
		t.Fatalf("unexpected alert count=%v err=%v", value, err)
	}
}

func TestLookupPathReturnsMissingForUnknownField(t *testing.T) {
	_, found := lookupPath(map[string]any{"data": map[string]any{}}, "data.success_rate")
	if found {
		t.Fatal("expected missing path")
	}
}
