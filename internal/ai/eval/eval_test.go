package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCasesReadsJSONLFilesFromDirectory(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "a.jsonl"), `{"id":"a","query":"first"}`+"\n")
	writeTestFile(t, filepath.Join(directory, "b.jsonl"), `{"id":"b","query":"second"}`+"\n")

	cases, err := LoadCases(directory)
	if err != nil {
		t.Fatalf("load cases: %v", err)
	}
	if len(cases) != 2 || cases[0].ID != "a" || cases[1].ID != "b" {
		t.Fatalf("unexpected cases: %#v", cases)
	}
}

func TestLoadCasesRejectsDuplicateIDsAcrossFiles(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "a.jsonl"), `{"id":"duplicate","query":"first"}`+"\n")
	writeTestFile(t, filepath.Join(directory, "b.jsonl"), `{"id":"duplicate","query":"second"}`+"\n")

	_, err := LoadCases(directory)
	if err == nil || !strings.Contains(err.Error(), "duplicate eval case id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestRepositoryEvalCasesMatchTheirModes(t *testing.T) {
	evalRoot := filepath.Join("..", "..", "..", "testdata", "eval")
	suites := []struct {
		mode Mode
		path string
	}{
		{mode: ModeContract, path: filepath.Join(evalRoot, "contract")},
		{mode: ModeRouting, path: filepath.Join(evalRoot, "routing")},
		{mode: ModeSmoke, path: filepath.Join(evalRoot, "smoke")},
	}

	for _, suite := range suites {
		cases, err := LoadCases(suite.path)
		if err != nil {
			t.Fatalf("load %s cases: %v", suite.mode, err)
		}
		for _, evalCase := range cases {
			if err = validateCaseForMode(evalCase, suite.mode); err != nil {
				t.Fatalf("validate %s case %s: %v", suite.mode, evalCase.ID, err)
			}
		}
	}
}

func TestContractModelUsesModelStepsInsteadOfExpectedTools(t *testing.T) {
	evalCase := Case{
		ID:       "contract-tool",
		Category: "contract",
		Query:    "find the runbook",
		ModelSteps: []ModelStep{
			{Type: "tool_call", Tool: "knowledge_agent", Arguments: map[string]any{"query": "payment runbook"}},
			{Type: "answer", Content: "use the payment runbook"},
		},
		ToolFixtures: []ToolFixture{{
			Name:   "knowledge_agent",
			Output: `{"success":true,"status":"success","evidence_id":"ev_runbook","message":"payment runbook evidence"}`,
		}},
		Expected: Expected{
			RequiredAgents:    []string{"knowledge_agent"},
			ArgumentContains:  map[string][]string{"knowledge_agent": {"payment runbook"}},
			AnswerContains:    []string{"payment runbook"},
			EvidenceContains:  []string{"runbook evidence"},
			RequireEvidenceID: true,
			MaxModelCalls:     2,
		},
	}

	result, err := RunCase(context.Background(), evalCase, RunOptions{
		Mode:               ModeContract,
		RunID:              "test",
		CaseTimeoutSeconds: 10,
	}, 1)
	if err != nil {
		t.Fatalf("run contract case: %v", err)
	}
	if !result.Scores.Pass {
		t.Fatalf("expected pass, got reasons=%v result=%#v", result.Scores.Reasons, result)
	}
	if result.Metrics.ModelCalls != 2 || result.Metrics.AgentCalls != 1 {
		t.Fatalf("unexpected execution metrics: %#v", result.Metrics)
	}
}

func TestContractCaseRequiresModelSteps(t *testing.T) {
	err := validateCaseForMode(Case{ID: "missing-steps", Query: "hello"}, ModeContract)
	if err == nil || !strings.Contains(err.Error(), "requires model_steps") {
		t.Fatalf("expected model step validation error, got %v", err)
	}
}

func TestRunCasesRepeatsEachCase(t *testing.T) {
	evalCase := Case{
		ID:         "repeat-contract",
		Query:      "hello",
		ModelSteps: []ModelStep{{Type: "answer", Content: "hello"}},
		Expected:   Expected{AnswerContains: []string{"hello"}},
	}

	results, err := RunCases(context.Background(), []Case{evalCase}, RunOptions{
		Mode:        ModeContract,
		RunID:       "repeat-test",
		Repetitions: 2,
	})
	if err != nil {
		t.Fatalf("run repeated cases: %v", err)
	}
	if len(results) != 2 || results[0].Attempt != 1 || results[1].Attempt != 2 {
		t.Fatalf("unexpected repeated results: %#v", results)
	}
}

func TestRoutingCaseRejectsScriptedModelSteps(t *testing.T) {
	err := validateCaseForMode(Case{
		ID:         "scripted-routing",
		Query:      "hello",
		ModelSteps: []ModelStep{{Type: "answer", Content: "hello"}},
	}, ModeRouting)
	if err == nil || !strings.Contains(err.Error(), "must not contain scripted model steps") {
		t.Fatalf("expected routing validation error, got %v", err)
	}
}

func TestScoreCaseChecksAgentArgumentsAndCallBudgets(t *testing.T) {
	result := EvalCaseResult{
		Answer: "callback timeout",
		Metrics: ExecutionMetrics{
			ModelCalls: 2,
			AgentCalls: 1,
			ToolCalls:  1,
		},
		ToolCalls: []ToolEvidence{{
			Name:       "payment_agent",
			Input:      `{"query":"check PAY-123 callback"}`,
			Output:     `{"success":true,"evidence_id":"ev_payment","message":"callback timeout"}`,
			Status:     "success",
			EvidenceID: "ev_payment",
		}},
	}
	evalCase := Case{Expected: Expected{
		RequiredAgents:    []string{"payment_agent"},
		ForbiddenAgents:   []string{"knowledge_agent"},
		ArgumentContains:  map[string][]string{"payment_agent": {"PAY-123"}},
		AnswerContains:    []string{"timeout"},
		EvidenceContains:  []string{"callback timeout"},
		RequireEvidenceID: true,
		MaxToolCalls:      1,
		MaxModelCalls:     2,
	}}

	score := ScoreCase(evalCase, result)
	if !score.Pass {
		t.Fatalf("expected score to pass, got %v", score.Reasons)
	}
}

func TestToolOutputStatusDetectsBusinessError(t *testing.T) {
	status, errorMessage, evidenceID := toolOutputStatus(`"{\"success\":false,\"status\":\"error\",\"error\":\"backend unavailable\"}"`)
	if status != "error" || errorMessage != "backend unavailable" || evidenceID != "" {
		t.Fatalf("unexpected status=%q error=%q evidence=%q", status, errorMessage, evidenceID)
	}
}

func TestWriteReports(t *testing.T) {
	directory := t.TempDir()
	results := []EvalCaseResult{{
		ID:      "c1",
		Attempt: 1,
		Mode:    ModeContract,
		Answer:  "ok",
		Metrics: ExecutionMetrics{ModelCalls: 1},
		Scores:  EvalScores{Pass: true},
	}}
	runResult, err := WriteReports(results, RunOptions{
		Mode:       ModeContract,
		ReportsDir: directory,
		RunID:      "test",
	})
	if err != nil {
		t.Fatalf("write reports: %v", err)
	}
	if _, err = os.Stat(runResult.JSONLPath); err != nil {
		t.Fatalf("missing jsonl report: %v", err)
	}
	if _, err = os.Stat(runResult.MarkdownPath); err != nil {
		t.Fatalf("missing markdown report: %v", err)
	}
}

func TestSummarizeReportsPerCaseStabilityAndAverageCost(t *testing.T) {
	results := []EvalCaseResult{
		{ID: "stable", Scores: EvalScores{Pass: true}, Metrics: ExecutionMetrics{ModelCalls: 2, ToolCalls: 1, TotalTokens: 100}},
		{ID: "stable", Scores: EvalScores{Pass: true}, Metrics: ExecutionMetrics{ModelCalls: 2, ToolCalls: 1, TotalTokens: 200}},
		{ID: "flaky", Scores: EvalScores{Pass: true}, Metrics: ExecutionMetrics{ModelCalls: 1, TotalTokens: 50}},
		{ID: "flaky", Scores: EvalScores{Pass: false}, Metrics: ExecutionMetrics{ModelCalls: 3, ToolCalls: 2, TotalTokens: 250}},
	}

	summary := Summarize(ModeRouting, results)
	if summary.Cases != 2 || summary.StableCases != 1 {
		t.Fatalf("unexpected stability summary: %#v", summary)
	}
	if summary.CasePassRates["stable"] != 1 || summary.CasePassRates["flaky"] != 0.5 {
		t.Fatalf("unexpected case pass rates: %#v", summary.CasePassRates)
	}
	if summary.AverageModelCalls != 2 || summary.AverageToolCalls != 1 || summary.AverageTotalTokens != 150 {
		t.Fatalf("unexpected average costs: %#v", summary)
	}
}

func TestSafetyFailureAlwaysFailsQualityGate(t *testing.T) {
	summary := RunSummary{
		Mode:                 ModeRouting,
		PassRate:             0.95,
		SafetyAttempts:       1,
		SafetyAttemptsPassed: 0,
	}

	applyQualityGate(&summary, 0.90)
	if summary.GatePass || summary.SafetyGatePass {
		t.Fatalf("expected safety failure to fail quality gate: %#v", summary)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
