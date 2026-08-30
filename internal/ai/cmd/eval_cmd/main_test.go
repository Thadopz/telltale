package main

import (
	"reflect"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/eval"
)

func TestDefaultCasesPathUsesSmokeCasesForSmokeMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeSmoke)
	if path != "testdata/eval/smoke" {
		t.Fatalf("expected smoke cases path, got %q", path)
	}
}

func TestSplitCaseIDsTrimsAndDropsEmptyValues(t *testing.T) {
	got := splitCaseIDs(" profile-outage, ,database-slow-query ")
	want := []string{"profile-outage", "database-slow-query"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split case ids=%v, want %v", got, want)
	}
}

func TestDefaultCasesPathUsesContractCasesForContractMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeContract)
	if path != "testdata/eval/contract" {
		t.Fatalf("expected contract cases path, got %q", path)
	}
}

func TestDefaultCasesPathUsesRoutingCasesForRoutingMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeRouting)
	if path != "testdata/eval/routing" {
		t.Fatalf("expected routing cases path, got %q", path)
	}
}

func TestDefaultCasesPathUsesContextStressCasesForContextStressMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeContextStress)
	if path != "testdata/eval/context_stress" {
		t.Fatalf("expected context stress cases path, got %q", path)
	}
}

func TestRoutingDefaultsToThreeRepetitions(t *testing.T) {
	if got := defaultRepetitions(eval.ModeRouting); got != 3 {
		t.Fatalf("expected three routing repetitions, got %d", got)
	}
}

func TestEvidenceAblationDefaultsToFiveRepetitions(t *testing.T) {
	if got := defaultRepetitions(eval.ModeEvidenceAblation); got != 5 {
		t.Fatalf("expected five evidence ablation repetitions, got %d", got)
	}
}
