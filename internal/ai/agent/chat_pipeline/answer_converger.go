package chat_pipeline

import (
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/ai/evidence"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

const answerFinalizerTimeout = 35 * time.Second

type AnswerConvergeInput struct {
	Question     string
	Answer       string
	Steps        []trace.AgentStep
	ToolEvidence []trace.ToolEvidence
	Finalize     func(context.Context, string, []trace.ToolEvidence) (string, error)
}

func ConvergeAnswer(ctx context.Context, input AnswerConvergeInput) (string, error) {
	answer := strings.TrimSpace(input.Answer)
	if answer != "" {
		if len(input.ToolEvidence) > 0 {
			return enforceEvidenceAnswerWithTools(ctx, input.Question, answer, input.Steps, input.ToolEvidence)
		}
		return enforceEvidenceAnswerFromTrace(ctx, answer, input.Steps)
	}
	if len(input.ToolEvidence) == 0 {
		return noEvidenceAnswer(), nil
	}

	finalize := input.Finalize
	if finalize == nil {
		finalize = finalizeAnswerWithModel
	}
	finalized, err := finalize(ctx, input.Question, input.ToolEvidence)
	if err == nil && strings.TrimSpace(finalized) != "" {
		return enforceEvidenceAnswerWithTools(ctx, input.Question, finalized, input.Steps, input.ToolEvidence)
	}
	return deterministicEvidenceAnswer(input.Question, input.ToolEvidence), nil
}

func enforceEvidenceAnswerWithTools(ctx context.Context, question string, answer string, steps []trace.AgentStep, tools []trace.ToolEvidence) (string, error) {
	if composed := composeCoverageAnswer(answer, steps); composed != "" {
		return composed, nil
	}
	available := evidenceSetFromToolEvidence(tools)
	return validateAndRepairEvidenceAnswer(ctx, answer, steps, available, func([]string) string {
		return deterministicEvidenceAnswer(question, tools)
	})
}

func validateAndRepairEvidenceAnswer(
	ctx context.Context,
	answer string,
	steps []trace.AgentStep,
	available evidence.EvidenceSet,
	fallback func([]string) string,
) (string, error) {
	validation := evidence.ValidateAnswer(answer, available)
	if validation.OK {
		return answer, nil
	}
	if len(available.EvidenceIDs) == 0 && len(available.FailedQueryIDs) == 0 {
		return answer, nil
	}
	repaired, err := repairEvidenceAnswer(ctx, answer, steps, validation.Reasons)
	if err != nil || strings.TrimSpace(repaired) == "" {
		return fallback(validation.Reasons), nil
	}
	// 模型修复也可能引入不存在的证据编号，因此返回前必须再次校验。
	if evidence.ValidateAnswer(repaired, available).OK {
		return repaired, nil
	}
	return fallback(validation.Reasons), nil
}

func finalizeAnswerWithModel(ctx context.Context, question string, tools []trace.ToolEvidence) (string, error) {
	model, err := newChatModel(ctx)
	if err != nil {
		return "", err
	}
	finalCtx, cancel := context.WithTimeout(ctx, answerFinalizerTimeout)
	defer cancel()
	msg, err := model.Generate(finalCtx, []*schema.Message{
		schema.SystemMessage(`You are the final-answer converger for an SRE incident agent.
Do not call tools. You cannot request more evidence.
Use only the provided tool evidence.
Return a concise final answer.
Every factual claim based on a tool must cite its evidence_id.`),
		schema.UserMessage("User question:\n" + strings.TrimSpace(question) + "\n\nTool evidence:\n" + compactToolEvidenceForFinalizer(tools)),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(msg.Content), nil
}

func evidenceSetFromToolEvidence(items []trace.ToolEvidence) evidence.EvidenceSet {
	sets := make([]evidence.EvidenceSet, 0, len(items)*2)
	for _, item := range items {
		sets = append(sets, evidence.ExtractIDs(item.OutputJSON))
		if item.EvidenceID != "" {
			sets = append(sets, evidence.EvidenceSet{EvidenceIDs: map[string]bool{item.EvidenceID: true}})
		}
		if item.FailedQueryID != "" {
			sets = append(sets, evidence.EvidenceSet{FailedQueryIDs: map[string]bool{item.FailedQueryID: true}})
		}
	}
	return evidence.Merge(sets...)
}

func compactToolEvidenceForFinalizer(items []trace.ToolEvidence) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString("- tool=")
		b.WriteString(item.ToolName)
		if item.EvidenceID != "" {
			b.WriteString(" evidence_id=")
			b.WriteString(item.EvidenceID)
		}
		if item.FailedQueryID != "" {
			b.WriteString(" failed_query_id=")
			b.WriteString(item.FailedQueryID)
		}
		if item.Summary != "" {
			b.WriteString(" summary=")
			b.WriteString(item.Summary)
		}
		if item.InputJSON != "" {
			b.WriteString(" input=")
			b.WriteString(trimForFinalizer(item.InputJSON, 300))
		}
		raw := toolEvidenceText(item)
		if raw != "" {
			b.WriteString("\n  output=")
			b.WriteString(trimForFinalizer(raw, 2200))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func deterministicEvidenceAnswer(question string, items []trace.ToolEvidence) string {
	var b strings.Builder
	b.WriteString("Verified evidence:\n")
	wrote := false
	for _, item := range items {
		if item.Envelope.Status == evidence.StatusSuccess && item.EvidenceID != "" && item.Envelope.CanUseAsEvidence {
			wrote = true
			b.WriteString("- ")
			b.WriteString(item.EvidenceID)
			b.WriteString(": ")
			b.WriteString(nonEmpty(item.Summary, "tool call succeeded"))
			b.WriteString(" (tool: ")
			b.WriteString(item.ToolName)
			b.WriteString(")\n")
		}
	}
	if !wrote {
		b.WriteString("- No successful evidence_id is available.\n")
	}
	b.WriteString("\nUnverified / evidence gaps:\n")
	gap := false
	for _, item := range items {
		if item.FailedQueryID != "" {
			gap = true
			b.WriteString("- ")
			b.WriteString(item.FailedQueryID)
			b.WriteString(": ")
			b.WriteString(nonEmpty(item.Summary, "tool call failed"))
			b.WriteByte('\n')
		}
	}
	if !gap {
		b.WriteString("- No failed query IDs were recorded.\n")
	}
	b.WriteString("\nAssessment:\n")
	b.WriteString("- The agent reached tool evidence but did not produce a model final answer; this conservative answer avoids inventing unsupported facts.\n")
	if strings.TrimSpace(question) != "" {
		b.WriteString("\nOriginal request:\n- ")
		b.WriteString(trimForFinalizer(question, 500))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func toolEvidenceText(item trace.ToolEvidence) string {
	if item.OutputJSON != "" {
		var env evidence.Envelope
		if err := json.Unmarshal([]byte(item.OutputJSON), &env); err == nil {
			if s, ok := env.Data.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
			if b, err := json.Marshal(env.Data); err == nil && string(b) != "null" {
				return string(b)
			}
		}
		return item.OutputJSON
	}
	if item.Envelope.Data != nil {
		if s, ok := item.Envelope.Data.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if b, err := json.Marshal(item.Envelope.Data); err == nil && string(b) != "null" {
			return string(b)
		}
	}
	return item.Summary
}
func trimForFinalizer(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func noEvidenceAnswer() string {
	return "Verified evidence:\n- No tool evidence was recorded.\n\nAssessment:\n- I cannot confirm the incident diagnosis without successful tool evidence.\n\nNext step:\n- Re-run targeted diagnostics and capture tool results before making a factual diagnosis."
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
