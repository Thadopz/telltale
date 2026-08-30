package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestEvidenceAblationSuiteHasTwelveCases(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "eval", "evidence_ablation")
	cases, err := LoadCases(path)
	if err != nil {
		t.Fatalf("load evidence ablation cases: %v", err)
	}
	if len(cases) != 12 {
		t.Fatalf("expected 12 fixed cases, got %d", len(cases))
	}
	for _, evalCase := range cases {
		if evalCase.Truth == nil || strings.TrimSpace(evalCase.Truth.RootCause) == "" {
			t.Fatalf("case %s is missing atomic truth", evalCase.ID)
		}
	}
}

func TestRawFixtureProjectionKeepsBusinessDataAndRemovesEvidenceMetadata(t *testing.T) {
	input := `{"success":true,"status":"success","evidence_id":"ev_secret","trust_level":"trusted","can_use_as_evidence":true,"data":{"service":"card-service","value":7},"observations":[{"kind":"metric_value"}],"scope":{"fields":{"service":"card-service"}},"summary":"seven"}`
	projected := rawFixtureProjection(input)
	if strings.Contains(projected, "evidence_id") || strings.Contains(projected, "trust_level") || strings.Contains(projected, "observations") || strings.Contains(projected, "scope") {
		t.Fatalf("projection leaked evidence metadata: %s", projected)
	}
	var source map[string]any
	var output map[string]any
	if err := json.Unmarshal([]byte(input), &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(projected), &output); err != nil {
		t.Fatal(err)
	}
	if sourceData, _ := json.Marshal(source["data"]); string(sourceData) != mustJSON(t, output["data"]) {
		t.Fatalf("business data changed: source=%s output=%s", sourceData, mustJSON(t, output["data"]))
	}
}

func TestEvidenceModeOrderIsBalanced(t *testing.T) {
	want := [][]string{
		{"full", "visible_unenforced", "raw_unenforced"},
		{"visible_unenforced", "raw_unenforced", "full"},
		{"raw_unenforced", "full", "visible_unenforced"},
	}
	for attempt := 1; attempt <= 3; attempt++ {
		got := evidenceModeOrder(attempt)
		if strings.Join(got, ",") != strings.Join(want[attempt-1], ",") {
			t.Fatalf("attempt %d order=%v want=%v", attempt, got, want[attempt-1])
		}
	}
}

func TestDatabaseOnlyAblationFixtureUsesDatabaseAgentScope(t *testing.T) {
	evalCase := Case{ToolFixtures: []ToolFixture{{Name: "db_slow_queries"}}}
	options := optionsForCase(evalCase, RunOptions{
		Mode:         ModeEvidenceAblation,
		EvidenceMode: EvidenceModeFull,
	}, nil, nil, &payloadRecorder{})
	if string(options.AgentScope) != "database_agent" {
		t.Fatalf("database fixture scope=%q, want database_agent", options.AgentScope)
	}

	evalCase.ToolFixtures = append(evalCase.ToolFixtures, ToolFixture{Name: "query_metrics"})
	options = optionsForCase(evalCase, RunOptions{
		Mode:         ModeEvidenceAblation,
		EvidenceMode: EvidenceModeFull,
	}, nil, nil, &payloadRecorder{})
	if options.AgentScope != "" {
		t.Fatalf("mixed fixtures unexpectedly narrowed to %q", options.AgentScope)
	}
}

func TestAblationNormalizerPromptRequiresAffirmativeForbiddenClaim(t *testing.T) {
	for _, required := range []string{"affirmatively presents", "denied", "negated", "unverified", "unsupported"} {
		if !strings.Contains(ablationNormalizerSystemPrompt, required) {
			t.Fatalf("normalizer prompt is missing polarity rule %q", required)
		}
	}
}

func TestAblationContextNoiseIsBounded(t *testing.T) {
	noise := ablationContextNoise(24000)
	if len(noise) != 24000 || !strings.Contains(noise, "UNRELATED_ARCHIVE") {
		t.Fatalf("unexpected bounded context noise: chars=%d", len(noise))
	}
}

func TestScriptedAblationChangesOnlyPresentationAndEnforcement(t *testing.T) {
	evalCase := scriptedAblationCase()
	results, err := RunCases(context.Background(), []Case{evalCase}, RunOptions{
		Mode:        ModeEvidenceAblation,
		RunID:       "ablation-test",
		Repetitions: 1,
	})
	if err != nil {
		t.Fatalf("run ablation: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected three arms, got %d", len(results))
	}
	byMode := map[EvidenceMode]EvalCaseResult{}
	for _, result := range results {
		byMode[result.EvidenceMode] = result
		if !result.Pilot {
			t.Fatalf("first five attempts must be marked as pilot")
		}
		if result.CanonicalPayloadDigest == "" || result.ModelVisiblePayloadDigest == "" {
			t.Fatalf("missing payload digest for %s", result.EvidenceMode)
		}
	}
	full := byMode[EvidenceModeFull]
	visible := byMode[EvidenceModeVisibleUnenforced]
	raw := byMode[EvidenceModeRawUnenforced]
	if full.ModelVisiblePayloadDigest != visible.ModelVisiblePayloadDigest {
		t.Fatalf("full and visible payloads drifted: %s != %s", full.ModelVisiblePayloadDigest, visible.ModelVisiblePayloadDigest)
	}
	if full.CanonicalPayloadDigest != raw.CanonicalPayloadDigest {
		t.Fatalf("canonical fixture changed across arms")
	}
	if raw.ModelVisiblePayloadDigest == full.ModelVisiblePayloadDigest {
		t.Fatalf("raw projection should differ from envelope")
	}
	if strings.Contains(raw.ToolCalls[0].Output, "evidence_id") || strings.Contains(raw.ToolCalls[0].Output, "observations") {
		t.Fatalf("raw tool output leaked evidence metadata: %s", raw.ToolCalls[0].Output)
	}
	if full.ClaimSet == nil || len(full.CoverageReports) == 0 {
		t.Fatalf("full arm did not run claim enforcement: %#v", full)
	}
	if visible.ClaimSet != nil || raw.ClaimSet != nil {
		t.Fatalf("unenforced arms unexpectedly ran claim enforcement")
	}
	if full.ConversationID == visible.ConversationID || visible.ConversationID == raw.ConversationID {
		t.Fatalf("arms share conversation state")
	}
}

func TestTruthDoesNotEnterAgentUserMessage(t *testing.T) {
	evalCase := scriptedAblationCase()
	evalCase.Truth.RequiredFacts[0].Description = "TRUTH_SENTINEL_NEVER_MODEL_VISIBLE"
	message := buildUserMessage(evalCase, "conversation")
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TRUTH_SENTINEL_NEVER_MODEL_VISIBLE") {
		t.Fatalf("truth leaked into user message: %s", encoded)
	}
}

func TestAblationStatisticsUsePairedRunsAndHolmCorrection(t *testing.T) {
	var results []EvalCaseResult
	for attempt := 1; attempt <= 20; attempt++ {
		pairID := "case/" + string(rune('a'+attempt))
		results = append(results,
			ablationResult(pairID, EvidenceModeFull, attempt <= 18),
			ablationResult(pairID, EvidenceModeVisibleUnenforced, attempt <= 14),
			ablationResult(pairID, EvidenceModeRawUnenforced, attempt <= 10),
		)
	}
	summary := SummarizeAblation(results, 2000, 7)
	if len(summary.Groups) != 3 || len(summary.Comparisons) != 3 {
		t.Fatalf("unexpected summary shape: %#v", summary)
	}
	if len(summary.Strata) == 0 || len(summary.ErrorCounts) == 0 {
		t.Fatalf("expected stratified and error-type results: %#v", summary)
	}
	comparison := summary.Comparisons[0]
	if comparison.Left != EvidenceModeFull || comparison.Right != EvidenceModeRawUnenforced || comparison.Pairs != 20 {
		t.Fatalf("unexpected primary comparison: %#v", comparison)
	}
	if comparison.AccuracyDelta != 0.4 || comparison.CILow <= 0 {
		t.Fatalf("unexpected paired effect: %#v", comparison)
	}
	for _, value := range summary.Comparisons {
		if value.HolmAdjustedP < value.McNemarP || value.HolmAdjustedP > 1 {
			t.Fatalf("invalid Holm adjustment: %#v", value)
		}
	}
}

func TestAblationExecutionErrorCannotBeCaseCorrect(t *testing.T) {
	evalCase := scriptedAblationCase()
	result := EvalCaseResult{
		Error:     "claim extraction failed",
		Answer:    "profile-service outage; HTTP 503",
		ToolCalls: []ToolEvidence{{Name: "query_profile_health"}},
	}
	claims := NormalizedAblationClaims{
		RootCause:      "profile_service_outage",
		ClaimedFactIDs: []string{"http_503"},
		Completed:      true,
	}
	score := scoreAblationCase(evalCase, result, claims)
	if score.TaskCompleted || score.CaseCorrect {
		t.Fatalf("execution error must be incomplete and incorrect: %#v", score)
	}
}

func TestBlindReviewPackContainsNoModeOrEvidenceID(t *testing.T) {
	directory := t.TempDir()
	result := ablationResult("case/1", EvidenceModeRawUnenforced, true)
	result.ID = "blind-case"
	result.Query = "diagnose"
	result.Answer = "raw_unenforced answer cites ev_private and coverage_status"
	report, err := WriteReports([]EvalCaseResult{result}, RunOptions{
		Mode:             ModeEvidenceAblation,
		ReportsDir:       directory,
		RunID:            "blind",
		BootstrapSamples: 10,
		RandomSeed:       1,
	})
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	content, err := os.ReadFile(report.BlindReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "raw_unenforced") || strings.Contains(text, "ev_private") || strings.Contains(text, "coverage_status") {
		t.Fatalf("blind review leaked experimental markers: %s", text)
	}
}

func scriptedAblationCase() Case {
	return Case{
		ID:       "scripted-ablation",
		Category: "profile_outage",
		Query:    "diagnose card-service",
		ModelSteps: []ModelStep{
			{Type: "tool_call", Tool: "query_profile_health", Arguments: map[string]any{"service": "card-service"}},
			{Type: "answer", Content: "profile-service outage; HTTP 503"},
		},
		ToolFixtures: []ToolFixture{{
			Name:   "query_profile_health",
			Output: `{"protocol_version":"2","status":"success","data_status":"present","data":{"service":"card-service","status":503},"evidence":{"evidence_id":"ev_fixture_test","provenance":{"source_kind":"runtime_metric","runtime_verified":true},"attestation":{"observed_at":"2026-08-12T02:00:00Z"},"observations":[{"kind":"metric_anomaly","subject":{"service":"card-service"},"fields":{"metric_name":"profile_failure","value":1},"summary":"HTTP 503"}]},"summary":"profile-service returned HTTP 503"}`,
		}},
		Claims: &evidence.ClaimSet{
			SchemaVersion: evidence.ClaimSchemaVersion,
			Claims: []evidence.CandidateClaim{{
				ClaimID:       "claim-profile",
				ClaimType:     evidence.ClaimMetricAnomaly,
				AssertionKind: evidence.AssertionFact,
				Subject:       evidence.SubjectRef{Service: "card-service"},
				Fields:        map[string]any{"metric_name": "profile_failure", "value": 1},
				EvidenceIDs:   []string{"ev_fixture_test"},
			}},
		},
		Truth: &AblationTruth{
			RootCause:         "profile_service_outage",
			RootCauseMatchAny: []string{"profile-service outage"},
			RequiredFacts: []AtomicTruth{{
				ID: "http_503", Description: "HTTP 503", MatchAny: []string{"503"},
			}},
		},
		Expected: Expected{RequiredTools: []string{"query_profile_health"}},
	}
}

func ablationResult(pairID string, mode EvidenceMode, correct bool) EvalCaseResult {
	score := &AblationCaseScore{
		CaseCorrect:          correct,
		AccuracyScore:        boolFloat(correct),
		FactF1:               boolFloat(correct),
		AbstentionCorrect:    true,
		TaskCompleted:        true,
		ToolSelectionCorrect: true,
	}
	return EvalCaseResult{
		ID:            "case",
		Attempt:       1,
		PairID:        pairID,
		EvidenceMode:  mode,
		AblationScore: score,
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
