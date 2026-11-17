package execution

import (
	"context"
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type diagnosticProposal struct {
	Candidates []runstate.Candidate `json:"candidates" jsonschema:"description=Two to four competing causal hypotheses grounded in visible evidence. Each check specifies an available direct metric tool with name argument, direction increased/decreased/changed/unchanged and how it distinguishes alternatives. State and reason are computed by the runtime."`
}

type singleDiagnosticProposal struct {
	Candidates []runstate.Candidate `json:"candidates" jsonschema:"minItems=1,maxItems=1,description=Exactly one evidence grounded causal hypothesis with scoped checks. State and reason are computed by the runtime."`
}

const hypothesisInstruction = `
Start with only TWO competing explanations for ONE service actually present in shared facts. Copy its service, RFC3339 start/end and evidence_id exactly. Do not add hypotheses for unobserved services. For a numeric check set metric to the full metric name and direction to increased, decreased, changed or unchanged. Example numeric check: {"tool":"<advertised metric tool>","metric":"<service>_cpu","direction":"increased","arguments":{"name":"<service>_cpu"},"discriminates":"Test CPU pressure against another resource cause"}. Never use direction observed with a nonempty metric. These are predictions, not asserted measurements.
After an initial ranking, further external queries must match a declared check exactly. Auxiliary log, trace or topology checks use metric="" and direction="observed"; they record query coverage, not support for causality. Include them in the hypothesis plan before querying. A pending check cannot be skipped at submission unless a real budget or permission failure blocks it.
Maintain competing diagnostic hypotheses in shared state. After observing metric candidates, use record_diagnostic_hypotheses to record two to four explicit alternatives grounded in evidence IDs and exact service/RFC3339 window. Plan direct metric checks that distinguish causes; include other resource indicators of the same service, including constant-baseline signals. A ranking seeds investigation, not proof. Read list_metrics before guessing names. State reasons are hypotheses, never verified causality. Execute pending checks with exactly the planned arguments; batch independent checks. Review the shared facts before requesting any repeated check. Compare downstream effects against resource causes. Conclude with a supported candidate or explicit uncertainty/gaps; do not equate a metric change with proven cause. Successful terminal submission ends the run. Never fabricate evidence IDs or supported/refuted statuses.
`

func diagnosticInstructions(ctx context.Context) string {
	instruction := hypothesisInstruction
	switch runstate.DiagnosticAblation(ctx) {
	case "free_query":
		instruction = strings.ReplaceAll(instruction, "After an initial ranking, further external queries must match a declared check exactly.", "External queries may freely explore observations without registering a check first.")
		instruction = strings.ReplaceAll(instruction, "Include them in the hypothesis plan before querying.", "Register useful checks as the investigation develops.")
	case "free_submit":
		instruction = strings.ReplaceAll(instruction, "A pending check cannot be skipped at submission unless a real budget or permission failure blocks it.", "Submission is allowed with pending checks or without a registered plan; state any remaining gaps.")
	case "single_hypothesis":
		instruction = strings.ReplaceAll(instruction, "only TWO competing explanations", "exactly ONE explanation")
		instruction = strings.ReplaceAll(instruction, "Maintain competing diagnostic hypotheses", "Maintain one diagnostic hypothesis")
		instruction = strings.ReplaceAll(instruction, "two to four explicit alternatives", "exactly one explicit explanation")
	case "no_feedback":
		instruction += "\nThe runtime records pending/completed checks only and provides no automatic prediction support/refutation feedback. Assess the raw observations yourself.\n"
	case "no_retention":
		instruction += "\nShared state snapshots are not repeated in context. Use the ordinary tool history to remember your registered plan and raw observations. Copy scope and evidence references from actual tool results.\n"
	}
	return instruction
}

func recordDiagnosticProposal(ctx context.Context, candidates []runstate.Candidate) (string, error) {
	if err := runstate.ProposeDiagnostic(ctx, candidates); err != nil {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "DIAGNOSTIC_PLAN_REJECTED", err.Error(), "Correct the proposal using observed evidence IDs, the exact scope and available check tools.")), nil
	}
	if runstate.DiagnosticAblation(ctx) == "no_retention" {
		return evidence.Marshal(evidence.NewData(map[string]any{"registered": true}, "Diagnostic plan registered.")), nil
	}
	encoded, err := json.Marshal(evidence.NewData(runstate.DiagnosticSnapshot(ctx), "Working hypotheses updated; this state is not independent evidence."))
	return string(encoded), err
}

func diagnosticProposalTool(ctx context.Context) (tool.InvokableTool, error) {
	if runstate.DiagnosticAblation(ctx) == "single_hypothesis" {
		return utils.InferOptionableTool(
			"record_diagnostic_hypotheses",
			"Record exactly one hypothesis and its scoped checks against actual observations.",
			func(ctx context.Context, input *singleDiagnosticProposal, _ ...tool.Option) (string, error) {
				return recordDiagnosticProposal(ctx, input.Candidates)
			},
		)
	}
	return utils.InferOptionableTool(
		"record_diagnostic_hypotheses",
		"Record competing hypotheses and discriminating checks; validate evidence IDs, service/window and tool names against actual observations. This updates working state and performs no external action.",
		func(ctx context.Context, input *diagnosticProposal, _ ...tool.Option) (string, error) {
			return recordDiagnosticProposal(ctx, input.Candidates)
		},
	)
}
