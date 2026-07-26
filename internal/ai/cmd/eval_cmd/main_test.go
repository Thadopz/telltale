package main

import (
	"SREagent/internal/ai/eval"
	"testing"
)

func TestDefaultCasesPathUsesSmokeCasesForSmokeMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeSmoke)
	if path != "testdata/eval/smoke" {
		t.Fatalf("expected smoke cases path, got %q", path)
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

func TestRoutingDefaultsToThreeRepetitions(t *testing.T) {
	if got := defaultRepetitions(eval.ModeRouting); got != 3 {
		t.Fatalf("expected three routing repetitions, got %d", got)
	}
}
