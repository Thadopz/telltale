package eval

import "testing"

func TestParseJudgeResultAcceptsJSONCodeFence(t *testing.T) {
	result, err := parseJudgeResult("```json\n{\"pass\":true,\"score\":0.85,\"reason\":\"complete and grounded\"}\n```")
	if err != nil {
		t.Fatalf("parse judge result: %v", err)
	}
	if !result.Pass || result.Score != 0.85 || result.Reason != "complete and grounded" {
		t.Fatalf("unexpected judge result: %#v", result)
	}
}

func TestScoreCaseFailsWhenJudgeRejectsAnswer(t *testing.T) {
	evalCase := Case{Expected: Expected{
		JudgeCriteria: []string{"answer resolves the request"},
		MinJudgeScore: 0.8,
	}}
	result := EvalCaseResult{
		Answer: "partial answer",
		Judge: JudgeResult{
			Enabled: true,
			Pass:    true,
			Score:   0.6,
			Reason:  "important step is missing",
		},
	}

	score := ScoreCase(evalCase, result)
	if score.Pass {
		t.Fatal("expected low judge score to fail")
	}
}
