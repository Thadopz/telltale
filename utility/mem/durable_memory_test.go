package mem

import (
	"context"
	"math"
	"testing"
)

func TestParseMemoryCandidatesAcceptsJSONCodeFence(t *testing.T) {
	raw := "```json\n[{\"memory_key\":\"preference.answer_detail\",\"kind\":\"preference\",\"content\":\"Prefer concise answers\",\"confidence\":0.95}]\n```"

	candidates, err := parseMemoryCandidates(raw)
	if err != nil {
		t.Fatalf("parse candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %d", len(candidates))
	}
	if candidates[0].MemoryKey != "preference.answer_detail" {
		t.Fatalf("unexpected memory key %q", candidates[0].MemoryKey)
	}
}

func TestValidateMemoryCandidateRejectsUnknownKey(t *testing.T) {
	candidate := MemoryCandidate{
		MemoryKey:  "preference.unknown",
		Kind:       "preference",
		Content:    "Prefer concise answers",
		Confidence: 0.95,
	}

	if _, valid := validateMemoryCandidate(context.Background(), candidate); valid {
		t.Fatal("expected unknown memory key to be rejected")
	}
}

func TestValidateMemoryCandidateRejectsSensitiveContent(t *testing.T) {
	candidate := MemoryCandidate{
		MemoryKey:  "environment.technology_stack",
		Kind:       "environment",
		Content:    "API key: secret-value",
		Confidence: 0.95,
	}

	if _, valid := validateMemoryCandidate(context.Background(), candidate); valid {
		t.Fatal("expected sensitive content to be rejected")
	}
}

func TestValidateMemoryCandidateRejectsTemporaryState(t *testing.T) {
	candidate := MemoryCandidate{
		MemoryKey:  "environment.technology_stack",
		Kind:       "environment",
		Content:    "当前错误率为 10%",
		Confidence: 0.95,
	}

	if _, valid := validateMemoryCandidate(context.Background(), candidate); valid {
		t.Fatal("expected temporary state to be rejected")
	}
}

func TestCosineSimilarityRecognizesSameDirection(t *testing.T) {
	similarity, available := cosineSimilarity(
		[]float32{1, 2, 3},
		[]float32{2, 4, 6},
	)
	if !available {
		t.Fatal("expected cosine similarity to be available")
	}
	if math.Abs(similarity-1) > 0.000001 {
		t.Fatalf("expected similarity 1, got %f", similarity)
	}
}

func TestCosineSimilarityRejectsDifferentDimensions(t *testing.T) {
	if _, available := cosineSimilarity([]float32{1}, []float32{1, 2}); available {
		t.Fatal("expected vectors with different dimensions to be rejected")
	}
}

func TestLowRiskMemoryKeysArePreferencesOnly(t *testing.T) {
	if !isLowRiskMemoryKey("preference.output_format") {
		t.Fatal("expected preference key to be low risk")
	}
	if isLowRiskMemoryKey("constraint.database_write") {
		t.Fatal("expected constraint key to require confirmation")
	}
}
