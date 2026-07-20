package main

import (
	"SREagent/internal/ai/eval"
	"testing"
)

func TestDefaultCasesPathUsesSmokeCasesForSmokeMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeSmoke)
	if path != "testdata/eval/smoke_cases.jsonl" {
		t.Fatalf("expected smoke cases path, got %q", path)
	}
}

func TestDefaultCasesPathUsesChatCasesForMockMode(t *testing.T) {
	path := defaultCasesPath(eval.ModeMock)
	if path != "testdata/eval/chat_cases.jsonl" {
		t.Fatalf("expected chat cases path, got %q", path)
	}
}
