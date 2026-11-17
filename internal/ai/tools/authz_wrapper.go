package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/knowledgecompression"
	"github.com/Thadopz/telltale/internal/ai/models"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/mem"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

type authorizedTool struct {
	inner                   tool.InvokableTool
	overridePermission      authz.Permission
	overrideAgentScopes     []authz.AgentScope
	overrideExecutionPolicy ExecutionPolicy
}

func wrapAuthorizedTool(inner tool.InvokableTool) tool.InvokableTool {
	return &authorizedTool{inner: inner}
}

func wrapAuthorizedToolWithPermission(inner tool.InvokableTool, permission authz.Permission) tool.InvokableTool {
	return &authorizedTool{inner: inner, overridePermission: permission}
}

func wrapAuthorizedToolWithPolicy(inner tool.InvokableTool, permission authz.Permission, scopes []authz.AgentScope, policy ExecutionPolicy) tool.InvokableTool {
	return &authorizedTool{
		inner:                   inner,
		overridePermission:      permission,
		overrideAgentScopes:     append([]authz.AgentScope(nil), scopes...),
		overrideExecutionPolicy: policy,
	}
}

func (t *authorizedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *authorizedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (result string, resultErr error) {
	info, err := t.inner.Info(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if resultErr == nil {
			runstate.RecordDiagnostic(ctx, info.Name, argumentsInJSON, evidence.Parse(result).Envelope)
		}
	}()
	call := buildToolCall(ctx, info.Name, argumentsInJSON, t.overridePermission)
	if len(t.overrideAgentScopes) > 0 {
		call.AllowedAgentScopes = append([]authz.AgentScope(nil), t.overrideAgentScopes...)
	}
	decision := authz.NewAuthorizer().Authorize(ctx, call)
	auditToolDecision(ctx, call, decision)
	if !decision.Allowed {
		return marshalToolError(nil, "tool authorization denied: "+decision.Reason), nil
	}
	terminal := false
	if configured, ok := t.inner.(*configuredMCPTool); ok {
		terminal = configured.terminal
	}
	if !terminal && !runstate.DiagnosticQueryAllowed(ctx, info.Name, argumentsInJSON) {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "DIAGNOSTIC_UNPLANNED_CHECK", "query does not match the declared diagnostic plan", "Record the exact check arguments and the evidence gap it addresses before querying.")), nil
	}
	policy := t.overrideExecutionPolicy
	if policy == (ExecutionPolicy{}) {
		policy = ExecutionPolicyFor(info.Name)
	}
	if call.Risk == authz.RiskWrite && !policy.Cancellable && !policy.OperationAware {
		return marshalToolError(nil, "write tool is not cancellable or operation-aware"), nil
	}

	timeout := toolCallTimeout(ctx)
	finishBudget, available := contextbudget.ReserveToolOutput(ctx, contextbudget.ToolLimits(ctx).OutputTokens)
	if !available {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "TOOL_BUDGET_EXHAUSTED", "shared turn budget exhausted", "Stop tool expansion; report current evidence and coverage gaps.")), nil
	}
	output, invokeErr := invokeToolWithPolicyTimeout(ctx, t.inner, info.Name, argumentsInJSON, timeout, policy, call.OperationID, opts...)
	if invokeErr == nil {
		if info.Name == "query_internal_docs" && knowledgecompression.NeedsSelectionInContext(ctx, evidence.Parse(output).Envelope) {
			strategy, strategyErr := knowledgecompression.Policy(ctx)
			if strategyErr != nil {
				finishBudget(0)
				return "", strategyErr
			}
			if strategy != "baseline" && contextString(ctx, "client_id") != "" {
				principal := authz.PrincipalFromContext(ctx)
				if err := mem.SaveKnowledgeAudit(ctx, mem.MemoryPrincipal{TenantID: principal.TenantID, UserID: principal.UserID}, contextString(ctx, "client_id"), argumentsInJSON, output); err != nil {
					finishBudget(0)
					return "", fmt.Errorf("persist original knowledge audit: %w", err)
				}
			}
		}
		output = IssueToolOutput(ctx, info.Name, output)
		evidence.RecordRuntimeAnchors(ctx, evidence.Parse(output).Envelope)
	}
	finishBudget(contextbudget.EstimateTextTokens(output))
	return output, invokeErr
}

// IssueToolOutput is the shared production and controlled-replay signing boundary.
func IssueToolOutput(ctx context.Context, toolName string, output string) string {
	limits := contextbudget.ToolLimits(ctx)
	if mode, err := appconfig.EvaluationEvidenceMode(ctx); err == nil && mode == "raw_unenforced" {
		projected := projectRawEvaluationOutput(output)
		if contextbudget.EstimateTextTokens(projected) > limits.OutputTokens {
			return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "RESULT_BUDGET_EXCEEDED", "raw evaluation result exceeds budget", "Stop expansion or narrow the query."))
		}
		return projected
	}
	parsed := evidence.Parse(output)
	if !parsed.IsJSON {
		return evidence.Marshal(evidence.NewUnstructuredResult(toolName))
	}
	if toolName == "query_internal_docs" {
		parsed.Envelope.Data = mem.RedactKnowledgePayload(parsed.Envelope.Data)
	} else {
		if toolName == "search_logs" || toolName == "search_traces" || toolName == "read_evidence" {
			parsed.Envelope.Data = redactTelemetryData(ctx, parsed.Envelope.Data)
		} else {
			parsed.Envelope.Data = mem.RedactToolPayload(parsed.Envelope.Data)
		}
	}
	if parsed.Envelope.Evidence != nil {
		for i := range parsed.Envelope.Evidence.Observations {
			obs := &parsed.Envelope.Evidence.Observations[i]
			if toolName == "query_internal_docs" && obs.Kind == evidence.ObservationRunbookRule {
				obs.Fields = mem.RedactKnowledgeFields(obs.Fields)
			} else {
				obs.Fields, _ = mem.RedactToolPayload(obs.Fields).(map[string]any)
			}
		}
	}
	parsed.Envelope = evidence.SanitizeUntrustedEnvelope(parsed.Envelope)
	if toolName == "query_internal_docs" {
		policy, err := knowledgecompression.Policy(ctx)
		if err != nil {
			return marshalToolError(err, "invalid knowledge compression policy")
		}
		if policy != "baseline" && knowledgecompression.NeedsSelectionInContext(ctx, parsed.Envelope) {
			selector, err := models.OpenAIForDeepSeekV3Quick(ctx)
			if err != nil {
				return marshalToolError(err, "knowledge compression unavailable; evidence gap")
			}
			selected, report, err := knowledgecompression.Select(ctx, parsed.Envelope, knowledgecompression.GoalFromContext(ctx), policy, selector)
			if err != nil {
				failure := evidence.NewError(err, "knowledge compression failed; original result was not presented, evidence gap")
				failure.Data = map[string]any{"compression": report}
				return evidence.Marshal(failure)
			}
			selected.Data = map[string]any{"compression": report}
			if contextbudget.EstimateTextTokens(evidence.Marshal(selected)) > limits.ContentTokens() {
				failure := evidence.NewFailure(evidence.StatusError, "KNOWLEDGE_SELECTION_BUDGET_EXCEEDED", "selected original paragraphs and metadata exceed signed tool budget", "Evidence gap; narrow the documentation query.")
				failure.Data = map[string]any{"compression": report}
				return evidence.Marshal(failure)
			}
			parsed.Envelope = selected
		}
	}
	parsed.Envelope = evidence.BoundOutputWithLimit(parsed.Envelope, limits.OutputTokens)
	if parsed.Envelope.Status != evidence.StatusSuccess {
		return evidence.Marshal(parsed.Envelope)
	}

	principal := authz.PrincipalFromContext(ctx)
	trustLevel := evidence.ContentTrustTrustedLocal
	if parsed.Envelope.Evidence != nil && parsed.Envelope.Evidence.Attestation.IssuerTrust == evidence.ContentTrustUntrustedExternal {
		trustLevel = evidence.ContentTrustUntrustedExternal
	}
	issued := evidence.IssueEnvelope(parsed.Envelope, evidence.IssueMetadata{
		Issuer:         "tool:" + toolName,
		RunID:          evidence.RunIDFromContext(ctx),
		ConversationID: contextString(ctx, "client_id"),
		TenantID:       principal.TenantID,
		TrustLevel:     trustLevel,
		ExpiresAfter:   10 * time.Minute,
	})
	output = evidence.Marshal(issued)
	if contextbudget.EstimateTextTokens(output) > limits.OutputTokens {
		return evidence.Marshal(evidence.NewFailure(evidence.StatusError, "RESULT_BUDGET_EXCEEDED", "signed result exceeds budget", "Stop expanding this query; narrow the scope."))
	}
	return output
}

func projectRawEvaluationOutput(output string) string {
	var source struct {
		Status  string          `json:"status"`
		Data    json.RawMessage `json:"data"`
		Summary string          `json:"summary"`
		Message string          `json:"message"`
	}
	if json.Unmarshal([]byte(output), &source) != nil {
		return `{"status":"protocol_error","summary":"raw evaluation tool result was not JSON"}`
	}
	if strings.TrimSpace(source.Status) == "" {
		source.Status = evidence.StatusUnknown
	}
	if strings.TrimSpace(source.Summary) == "" {
		source.Summary = source.Message
	}
	projection := struct {
		Status  string          `json:"status"`
		Data    json.RawMessage `json:"data,omitempty"`
		Summary string          `json:"summary,omitempty"`
	}{Status: source.Status, Data: source.Data, Summary: source.Summary}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return `{"status":"protocol_error","summary":"raw evaluation projection failed"}`
	}
	return string(encoded)
}

type toolRunResult struct {
	output string
	err    error
}

func invokeToolWithTimeout(
	ctx context.Context,
	inner tool.InvokableTool,
	toolName string,
	argumentsInJSON string,
	timeout time.Duration,
	opts ...tool.Option,
) (string, error) {
	return invokeToolWithPolicyTimeout(ctx, inner, toolName, argumentsInJSON, timeout, ExecutionPolicy{ReadOnly: true, Cancellable: true, Idempotent: true}, "", opts...)
}

func invokeToolWithPolicyTimeout(
	ctx context.Context,
	inner tool.InvokableTool,
	toolName string,
	argumentsInJSON string,
	timeout time.Duration,
	policy ExecutionPolicy,
	operationID string,
	opts ...tool.Option,
) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan toolRunResult, 1)
	go func() {
		output, err := inner.InvokableRun(runCtx, argumentsInJSON, opts...)
		resultCh <- toolRunResult{output: output, err: err}
	}()

	select {
	case result := <-resultCh:
		if errors.Is(result.err, context.DeadlineExceeded) && ctx.Err() == nil {
			return timeoutOutputForPolicy(toolName, timeout, policy, operationID), nil
		}
		return result.output, result.err
	case <-runCtx.Done():
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return timeoutOutputForPolicy(toolName, timeout, policy, operationID), nil
	}
}

func timeoutOutputForPolicy(toolName string, timeout time.Duration, policy ExecutionPolicy, operationID string) string {
	if !policy.ReadOnly && policy.OperationAware {
		return evidence.Marshal(evidence.NewUnknownOperation(toolName, operationID, timeout))
	}
	return toolTimeoutOutput(toolName, timeout)
}

func toolTimeoutOutput(toolName string, timeout time.Duration) string {
	message := fmt.Sprintf("tool %s timed out after %s", toolName, timeout)
	return marshalToolError(context.DeadlineExceeded, message)
}

func toolAllowedForRegistration(ctx context.Context, toolName string, overridePermission authz.Permission) bool {
	call := buildToolCall(ctx, toolName, "", overridePermission)
	decision := authz.NewAuthorizer().Authorize(ctx, call)
	auditToolDecision(ctx, call, decision)
	return decision.Allowed
}

func buildToolCall(ctx context.Context, toolName string, argumentsInJSON string, overridePermission authz.Permission) authz.ToolCall {
	meta, _ := LookupMetadata(toolName)
	permission := meta.Permission
	if overridePermission != "" {
		permission = overridePermission
	}
	risk := meta.Risk
	requiresConfirm := meta.RequiresConfirm
	operation := ""
	operationID := operationIDFromArgs(argumentsInJSON)

	if toolName == "mysql_crud" {
		operation = mysqlOperateTypeFromArgs(argumentsInJSON)
		if IsWriteOperation(operation) {
			permission = authz.PermDatabaseWrite
			risk = authz.RiskWrite
			requiresConfirm = true
		} else {
			permission = authz.PermDatabaseRead
			risk = authz.RiskReadOnly
			requiresConfirm = false
		}
	}

	return authz.ToolCall{
		Name:               toolName,
		Permission:         permission,
		Risk:               risk,
		RequiresConfirm:    requiresConfirm,
		Confirmed:          authz.IsToolCallConfirmed(ctx, toolName, argumentsInJSON),
		AgentScope:         authz.AgentScopeFromContext(ctx),
		AllowedAgentScopes: meta.AllowedAgentScopes,
		RequiredCapability: meta.RequiredCapability,
		ConversationID:     contextString(ctx, "client_id"),
		ArgumentsInJSON:    argumentsInJSON,
		Operation:          operation,
		OperationID:        operationID,
		ServerWriteEnabled: mysqlWriteEnabled(ctx),
	}
}

func operationIDFromArgs(argumentsInJSON string) string {
	if strings.TrimSpace(argumentsInJSON) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return ""
	}
	value, _ := payload["operation_id"].(string)
	return strings.TrimSpace(value)
}

func mysqlOperateTypeFromArgs(argumentsInJSON string) string {
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "query"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return "query"
	}
	if raw, ok := payload["operate_type"].(string); ok {
		op := strings.ToLower(strings.TrimSpace(raw))
		if op != "" {
			return op
		}
	}
	return "query"
}

func auditToolDecision(ctx context.Context, call authz.ToolCall, decision authz.Decision) {
	if authz.QuietLogging(ctx) {
		return
	}
	principal := authz.PrincipalFromContext(ctx)
	g.Log().Infof(ctx,
		"[authz] user=%s tenant=%s roles=%s agent_scope=%s tool=%s permission=%s risk=%s operation=%s operation_id=%s conversation_id=%s allowed=%t reason=%s",
		principal.UserID,
		principal.TenantID,
		strings.Join(principal.Roles, ","),
		call.AgentScope,
		call.Name,
		call.Permission,
		call.Risk,
		call.Operation,
		call.OperationID,
		call.ConversationID,
		decision.Allowed,
		decision.Reason,
	)
}

func contextString(ctx context.Context, key string) string {
	if v, ok := ctx.Value(key).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
