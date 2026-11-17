package mem

import (
	"context"
	"encoding/json"
	"github.com/Thadopz/telltale/internal/ai/runstate"
	"strings"
)

type ContextLayers struct {
	SessionState  string
	DurableMemory string
	ToolResults   string
}

type sessionStateRow struct {
	Goal   string `json:"goal"`
	Status string `json:"status"`
	State  string `json:"state"`
}

func LoadContextLayers(ctx context.Context, userID string, conversationID string) (ContextLayers, error) {
	principal := MemoryPrincipal{TenantID: "local", UserID: userID}
	return loadContextLayersForScope(ctx, principal, normalizeMemoryScope(userID, conversationID), "")
}

func LoadContextLayersForQuery(ctx context.Context, principal MemoryPrincipal, conversationID string, query string) (ContextLayers, error) {
	scope := normalizeMemoryScope(principal.UserID, TenantConversationID(principal, conversationID))
	return loadContextLayersForScope(ctx, principal, scope, query)
}

func loadContextLayersForScope(ctx context.Context, principal MemoryPrincipal, scope memoryScope, query string) (ContextLayers, error) {
	var layers ContextLayers

	if scope.StorageID != "" {
		state, err := loadSessionStateRow(ctx, scope.StorageID)
		if err != nil {
			return layers, err
		}
		layers.SessionState = formatSessionState(state)
		layers.ToolResults, err = loadRecentToolResults(ctx, scope.StorageID)
		if err != nil {
			return layers, err
		}
	}

	if strings.TrimSpace(scope.UserID) == "" {
		return layers, nil
	}
	if ConfiguredMemory(ctx).Mode == MemoryModeActive {
		memoryContext, err := loadProfileEventContext(ctx, principal, query)
		if err != nil {
			return layers, err
		}
		layers.DurableMemory = memoryContext
		return layers, nil
	}

	return layers, nil
}

func formatSessionState(state sessionStateRow) string {
	var lines []string
	var payload sessionStatePayload
	if json.Unmarshal([]byte(state.State), &payload) == nil && payload.DiagnosticState != "" {
		var diagnostic runstate.Diagnostic
		if json.Unmarshal([]byte(payload.DiagnosticState), &diagnostic) == nil {
			lines = append(lines, runstate.DiagnosticMemory(&diagnostic))
		}
	}
	if strings.TrimSpace(state.Goal) != "" {
		lines = append(lines, "Goal: "+strings.TrimSpace(state.Goal))
	}
	if strings.TrimSpace(state.Status) != "" {
		lines = append(lines, "Status: "+strings.TrimSpace(state.Status))
	}
	if strings.TrimSpace(state.State) != "" {
		lines = append(lines, "State: "+strings.TrimSpace(state.State))
	}
	return strings.Join(lines, "\n")
}
