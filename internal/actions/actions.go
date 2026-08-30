package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/tools"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/gogf/gf/v2/frame/g"
)

const actionTTL = 10 * time.Minute

const (
	maxActionArgumentsBytes  = 64 * 1024
	maxActionIdentifierChars = 128
)

type PreparedAction struct {
	Token           string    `json:"token"`
	ToolName        string    `json:"tool_name"`
	OperationID     string    `json:"operation_id,omitempty"`
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
	OperationID   string `json:"operation_id,omitempty"`
	ArgumentsHash string `json:"arguments_hash"`
	Status        string `json:"status"`
	Output        string `json:"output"`
}

type OperationStatus string

const (
	OperationPending  OperationStatus = "pending"
	OperationRunning  OperationStatus = "running"
	OperationUnknown  OperationStatus = "unknown"
	OperationComplete OperationStatus = "complete"
	OperationFailed   OperationStatus = "failed"
)

type OperationRecord struct {
	OperationID    string
	ConversationID string
	UserID         string
	TenantID       string
	ToolName       string
	Status         OperationStatus
	ResultSummary  string
	EvidenceID     string
	UpdatedAt      time.Time
}

type pendingAction struct {
	PreparedAction
	UserID        string
	TenantID      string
	RolesKey      string
	ArgumentsJSON string
	Confirmed     bool
}

type actionStore interface {
	Save(context.Context, pendingAction) error
	Load(context.Context, string) (pendingAction, error)
	Confirm(context.Context, string) error
	ConsumeConfirmed(context.Context, string, time.Time) error
	Delete(context.Context, string) error
	Prune(context.Context, time.Time) error
}

type operationStore interface {
	SaveOperation(context.Context, OperationRecord) error
	UpdateOperation(context.Context, OperationRecord) error
	LoadOperation(context.Context, string) (OperationRecord, error)
}

type memoryActionStore struct {
	sync.Mutex
	actions    map[string]pendingAction
	operations map[string]OperationRecord
}

var store = &memoryActionStore{actions: make(map[string]pendingAction), operations: make(map[string]OperationRecord)}
var configuredStore actionStore = store

// Configure selects the ActionStore for the standalone runtime. Memory is the
// default and SQLite provides reliable recovery across process restarts.
func Configure(ctx context.Context) error {
	mode := "memory"
	if value, err := g.Cfg().Get(ctx, "actions.store"); err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		mode = strings.ToLower(strings.TrimSpace(value.String()))
	}
	switch mode {
	case "memory":
		configuredStore = store
		return nil
	case "sqlite":
		configuredStore = &sqliteActionStore{}
		return nil
	default:
		return fmt.Errorf("unsupported actions.store %q; use memory or sqlite", mode)
	}
}

func Prepare(ctx context.Context, toolName string, argumentsJSON string) (PreparedAction, error) {
	toolName, argumentsJSON, err := cleanToolRequest(toolName, argumentsJSON)
	if err != nil {
		return PreparedAction{}, fmt.Errorf("tool_name and arguments_json are required")
	}

	call, decision := tools.DescribeToolCall(ctx, toolName, argumentsJSON)
	if err := validateHighRiskToolCall(toolName, call, decision); err != nil {
		return PreparedAction{}, err
	}
	if call.Risk == authz.RiskWrite && strings.TrimSpace(call.OperationID) == "" {
		return PreparedAction{}, fmt.Errorf("write operation requires operation_id")
	}
	if call.Risk == authz.RiskWrite {
		if err = validateActionIdentifier("operation_id", call.OperationID); err != nil {
			return PreparedAction{}, err
		}
	}

	token, err := randomToken()
	if err != nil {
		return PreparedAction{}, err
	}

	prepared := newPreparedAction(token, toolName, argumentsJSON, call.Risk, call.OperationID)
	if err = savePendingAction(ctx, prepared, argumentsJSON); err != nil {
		return PreparedAction{}, err
	}
	return prepared, nil
}

func validateHighRiskToolCall(toolName string, call authz.ToolCall, decision authz.Decision) error {
	if call.Risk != authz.RiskWrite || !call.RequiresConfirm {
		return fmt.Errorf("tool %s does not require high-risk confirmation", toolName)
	}
	if !decision.Allowed && decision.Reason != "write operation requires confirmation" {
		return fmt.Errorf("tool authorization denied: %s", decision.Reason)
	}
	return nil
}

func newPreparedAction(token string, toolName string, argumentsJSON string, risk authz.RiskLevel, operationID string) PreparedAction {
	return PreparedAction{
		Token:           token,
		ToolName:        toolName,
		OperationID:     strings.TrimSpace(operationID),
		ArgumentsHash:   authz.HashArguments(argumentsJSON),
		Risk:            string(risk),
		RequiresConfirm: true,
		ExpiresAt:       time.Now().Add(actionTTL),
	}
}

func savePendingAction(ctx context.Context, prepared PreparedAction, argumentsJSON string) error {
	principal := authz.PrincipalFromContext(ctx)
	action := pendingAction{
		PreparedAction: prepared,
		UserID:         principal.UserID,
		TenantID:       principal.TenantID,
		RolesKey:       rolesKey(principal.Roles),
		ArgumentsJSON:  argumentsJSON,
	}
	return configuredStore.Save(ctx, action)
}

func Confirm(ctx context.Context, token string, argumentsHash string) (ConfirmedAction, error) {
	token = strings.TrimSpace(token)
	argumentsHash = strings.TrimSpace(argumentsHash)
	if token == "" || argumentsHash == "" {
		return ConfirmedAction{}, fmt.Errorf("token and arguments_hash are required")
	}

	principal := authz.PrincipalFromContext(ctx)
	action, err := findPendingAction(ctx, token)
	if err != nil {
		return ConfirmedAction{}, fmt.Errorf("action token not found or expired")
	}
	if err = validatePrincipal(action, principal); err != nil {
		return ConfirmedAction{}, err
	}
	if action.ArgumentsHash != argumentsHash {
		return ConfirmedAction{}, fmt.Errorf("arguments hash mismatch")
	}
	if err = configuredStore.Confirm(ctx, token); err != nil {
		return ConfirmedAction{}, err
	}
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
	action, err := findPendingAction(ctx, token)
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

	if err := consumeConfirmedAction(ctx, token); err != nil {
		return ExecuteResult{}, err
	}
	operationID := operationIDFromArguments(argumentsJSON)
	operation := OperationRecord{
		OperationID:    operationID,
		ConversationID: contextValueString(ctx, "client_id"),
		UserID:         principal.UserID,
		TenantID:       principal.TenantID,
		ToolName:       toolName,
		Status:         OperationRunning,
		UpdatedAt:      time.Now().UTC(),
	}
	if operationID != "" {
		if err := saveOperation(ctx, operation); err != nil {
			return ExecuteResult{}, err
		}
	}
	out, err := tools.InvokeConfirmedTool(ctx, toolName, argumentsJSON, token)
	if err != nil {
		operation.Status = OperationFailed
		operation.ResultSummary = err.Error()
		if updateErr := updateOperation(ctx, operation); updateErr != nil {
			return ExecuteResult{ToolName: toolName, OperationID: operationID, ArgumentsHash: action.ArgumentsHash, Status: string(operation.Status)}, fmt.Errorf("tool execution failed and operation state was not saved: %w; state error: %v", err, updateErr)
		}
		return ExecuteResult{ToolName: toolName, OperationID: operationID, ArgumentsHash: action.ArgumentsHash, Status: string(operation.Status)}, err
	}
	parsed := evidence.Parse(out).Envelope
	if record, ok := parsed.UsableEvidence(); ok {
		operation.EvidenceID = record.EvidenceID
	}
	operation.ResultSummary = parsed.Summary
	if parsed.Status == evidence.StatusUnknown {
		operation.Status = OperationUnknown
	} else if parsed.Status == evidence.StatusSuccess {
		operation.Status = OperationComplete
	} else {
		operation.Status = OperationFailed
	}
	operation.UpdatedAt = time.Now().UTC()
	if updateErr := updateOperation(ctx, operation); updateErr != nil {
		return ExecuteResult{ToolName: toolName, OperationID: operationID, ArgumentsHash: action.ArgumentsHash, Status: string(operation.Status), Output: out}, fmt.Errorf("tool execution completed but operation state was not saved: %w", updateErr)
	}
	return ExecuteResult{
		ToolName:      toolName,
		OperationID:   operationID,
		ArgumentsHash: action.ArgumentsHash,
		Status:        string(operation.Status),
		Output:        out,
	}, nil
}

func saveOperation(ctx context.Context, operation OperationRecord) error {
	if strings.TrimSpace(operation.OperationID) == "" {
		return nil
	}
	store, ok := configuredStore.(operationStore)
	if !ok {
		return nil
	}
	return store.SaveOperation(ctx, operation)
}

func updateOperation(ctx context.Context, operation OperationRecord) error {
	if strings.TrimSpace(operation.OperationID) == "" {
		return nil
	}
	store, ok := configuredStore.(operationStore)
	if !ok {
		return nil
	}
	return store.UpdateOperation(ctx, operation)
}

func GetOperation(ctx context.Context, operationID string) (OperationRecord, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return OperationRecord{}, fmt.Errorf("operation_id is required")
	}
	store, ok := configuredStore.(operationStore)
	if !ok {
		return OperationRecord{}, fmt.Errorf("operation state store is unavailable")
	}
	return store.LoadOperation(ctx, operationID)
}

func operationIDFromArguments(argumentsJSON string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &payload); err != nil {
		return ""
	}
	value, _ := payload["operation_id"].(string)
	return strings.TrimSpace(value)
}

func contextValueString(ctx context.Context, key string) string {
	if value, ok := ctx.Value(key).(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func cleanToolRequest(toolName string, argumentsJSON string) (string, string, error) {
	toolName = strings.TrimSpace(toolName)
	argumentsJSON = strings.TrimSpace(argumentsJSON)
	if toolName == "" || argumentsJSON == "" {
		return "", "", fmt.Errorf("tool_name and arguments_json are required")
	}
	if err := validateActionIdentifier("tool_name", toolName); err != nil {
		return "", "", err
	}
	if len(argumentsJSON) > maxActionArgumentsBytes {
		return "", "", fmt.Errorf("arguments_json exceeds the %d byte limit", maxActionArgumentsBytes)
	}
	if !json.Valid([]byte(argumentsJSON)) {
		return "", "", fmt.Errorf("arguments_json must contain one valid JSON value")
	}
	return toolName, argumentsJSON, nil
}

func validateActionIdentifier(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len([]rune(value)) > maxActionIdentifierChars {
		return fmt.Errorf("%s exceeds the %d character limit", field, maxActionIdentifierChars)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("-_.:@", character) {
			continue
		}
		return fmt.Errorf("%s contains an invalid character", field)
	}
	return nil
}

func findPendingAction(ctx context.Context, token string) (pendingAction, error) {
	if err := configuredStore.Prune(ctx, time.Now()); err != nil {
		return pendingAction{}, err
	}
	return configuredStore.Load(ctx, token)
}

func consumeConfirmedAction(ctx context.Context, token string) error {
	if err := configuredStore.ConsumeConfirmed(ctx, token, time.Now()); err != nil {
		return fmt.Errorf("action token not found or expired")
	}
	return nil
}

func validatePrincipal(action pendingAction, principal authz.Principal) error {
	if action.UserID != principal.UserID || action.TenantID != principal.TenantID || action.RolesKey != rolesKey(principal.Roles) {
		return fmt.Errorf("principal mismatch")
	}
	return nil
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
