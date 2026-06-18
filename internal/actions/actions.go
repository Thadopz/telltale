package actions

import (
	"SREagent/internal/ai/tools"
	"SREagent/internal/authz"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const actionTTL = 10 * time.Minute

type PreparedAction struct {
	Token           string    `json:"token"`
	ToolName        string    `json:"tool_name"`
	ArgumentsHash   string    `json:"arguments_hash"`
	Risk            string    `json:"risk"`
	RequiresConfirm bool      `json:"requires_confirm"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type ConfirmedAction struct {
	Token         string `json:"token"`
	ToolName      string `json:"tool_name"`
	ArgumentsHash string `json:"arguments_hash"`
	Status        string `json:"status"`
}

type ExecuteResult struct {
	ToolName      string `json:"tool_name"`
	ArgumentsHash string `json:"arguments_hash"`
	Output        string `json:"output"`
}

type pendingAction struct {
	PreparedAction
	UserID        string
	TenantID      string
	RolesKey      string
	ArgumentsJSON string
	Confirmed     bool
}

var store = struct {
	sync.Mutex
	actions map[string]pendingAction
}{actions: make(map[string]pendingAction)}

func Prepare(ctx context.Context, toolName string, argumentsJSON string) (PreparedAction, error) {
	toolName, argumentsJSON, err := cleanToolRequest(toolName, argumentsJSON)
	if err != nil {
		return PreparedAction{}, fmt.Errorf("tool_name and arguments_json are required")
	}

	call, decision := tools.DescribeToolCall(ctx, toolName, argumentsJSON)
	if err := validateHighRiskToolCall(toolName, call, decision); err != nil {
		return PreparedAction{}, err
	}

	token, err := randomToken()
	if err != nil {
		return PreparedAction{}, err
	}

	prepared := newPreparedAction(token, toolName, argumentsJSON, call.Risk)
	savePendingAction(ctx, prepared, argumentsJSON)
	return prepared, nil
}

func validateHighRiskToolCall(toolName string, call authz.ToolCall, decision authz.Decision) error {
	// 只有高风险写操作才进入“准备-确认-执行”流程，读操作应直接调用工具。
	if call.Risk != authz.RiskWrite || !call.RequiresConfirm {
		return fmt.Errorf("tool %s does not require high-risk confirmation", toolName)
	}
	if !decision.Allowed && decision.Reason != "write operation requires confirmation" {
		return fmt.Errorf("tool authorization denied: %s", decision.Reason)
	}
	return nil
}

func newPreparedAction(token string, toolName string, argumentsJSON string, risk authz.RiskLevel) PreparedAction {
	return PreparedAction{
		Token:           token,
		ToolName:        toolName,
		ArgumentsHash:   authz.HashArguments(argumentsJSON),
		Risk:            string(risk),
		RequiresConfirm: true,
		ExpiresAt:       time.Now().Add(actionTTL),
	}
}

func savePendingAction(ctx context.Context, prepared PreparedAction, argumentsJSON string) {
	principal := authz.PrincipalFromContext(ctx)
	store.Lock()
	pruneExpiredLocked(time.Now())
	store.actions[prepared.Token] = pendingAction{
		PreparedAction: prepared,
		UserID:         principal.UserID,
		TenantID:       principal.TenantID,
		RolesKey:       rolesKey(principal.Roles),
		ArgumentsJSON:  argumentsJSON,
	}
	store.Unlock()
}

func Confirm(ctx context.Context, token string, argumentsHash string) (ConfirmedAction, error) {
	token = strings.TrimSpace(token)
	argumentsHash = strings.TrimSpace(argumentsHash)
	if token == "" || argumentsHash == "" {
		return ConfirmedAction{}, fmt.Errorf("token and arguments_hash are required")
	}

	principal := authz.PrincipalFromContext(ctx)
	store.Lock()
	defer store.Unlock()
	pruneExpiredLocked(time.Now())
	action, ok := store.actions[token]
	if !ok {
		return ConfirmedAction{}, fmt.Errorf("action token not found or expired")
	}
	if err := validatePrincipal(action, principal); err != nil {
		return ConfirmedAction{}, err
	}
	if action.ArgumentsHash != argumentsHash {
		return ConfirmedAction{}, fmt.Errorf("arguments hash mismatch")
	}
	action.Confirmed = true
	store.actions[token] = action
	return ConfirmedAction{
		Token:         action.Token,
		ToolName:      action.ToolName,
		ArgumentsHash: action.ArgumentsHash,
		Status:        "confirmed",
	}, nil
}

func Execute(ctx context.Context, token string, toolName string, argumentsJSON string) (ExecuteResult, error) {
	token = strings.TrimSpace(token)
	toolName, argumentsJSON, err := cleanToolRequest(toolName, argumentsJSON)
	if token == "" || err != nil {
		return ExecuteResult{}, fmt.Errorf("token, tool_name, and arguments_json are required")
	}

	principal := authz.PrincipalFromContext(ctx)
	action, err := findPendingAction(token)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("action token not found or expired")
	}
	if err := validatePrincipal(action, principal); err != nil {
		return ExecuteResult{}, err
	}
	if !action.Confirmed {
		return ExecuteResult{}, fmt.Errorf("action token has not been confirmed")
	}
	if action.ToolName != toolName {
		return ExecuteResult{}, fmt.Errorf("tool mismatch")
	}
	if action.ArgumentsHash != authz.HashArguments(argumentsJSON) || action.ArgumentsJSON != argumentsJSON {
		return ExecuteResult{}, fmt.Errorf("arguments mismatch")
	}

	// 执行前删除 token，避免同一个确认令牌被重复使用。
	if err := consumeConfirmedAction(token); err != nil {
		return ExecuteResult{}, err
	}

	out, err := tools.InvokeConfirmedTool(ctx, toolName, argumentsJSON, token)
	if err != nil {
		return ExecuteResult{}, err
	}
	return ExecuteResult{
		ToolName:      toolName,
		ArgumentsHash: action.ArgumentsHash,
		Output:        out,
	}, nil
}

func cleanToolRequest(toolName string, argumentsJSON string) (string, string, error) {
	toolName = strings.TrimSpace(toolName)
	argumentsJSON = strings.TrimSpace(argumentsJSON)
	if toolName == "" || argumentsJSON == "" {
		return "", "", fmt.Errorf("tool_name and arguments_json are required")
	}
	return toolName, argumentsJSON, nil
}

func findPendingAction(token string) (pendingAction, error) {
	store.Lock()
	defer store.Unlock()

	pruneExpiredLocked(time.Now())
	action, ok := store.actions[token]
	if !ok {
		return pendingAction{}, fmt.Errorf("action token not found or expired")
	}
	return action, nil
}

func consumeConfirmedAction(token string) error {
	store.Lock()
	defer store.Unlock()

	current, ok := store.actions[token]
	if !ok || !current.Confirmed {
		return fmt.Errorf("action token not found or expired")
	}
	delete(store.actions, token)
	return nil
}

func validatePrincipal(action pendingAction, principal authz.Principal) error {
	if action.UserID != principal.UserID || action.TenantID != principal.TenantID || action.RolesKey != rolesKey(principal.Roles) {
		return fmt.Errorf("principal mismatch")
	}
	return nil
}

func pruneExpiredLocked(now time.Time) {
	for token, action := range store.actions {
		if now.After(action.ExpiresAt) {
			delete(store.actions, token)
		}
	}
}

func rolesKey(roles []string) string {
	roles = append([]string(nil), roles...)
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
