package mem

import (
	"context"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

const durableMemoryContextLimit = 10

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

type durableMemoryRow struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
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

	var memories []durableMemoryRow
	err := g.DB().Model("durable_memories").
		Ctx(ctx).
		Fields("kind", "content").
		Where("user_id", scope.UserID).
		Where("status", "active").
		OrderDesc("updated_at").
		Limit(durableMemoryContextLimit).
		Scan(&memories)
	if err != nil {
		return layers, err
	}
	layers.DurableMemory = formatDurableMemories(memories)
	return layers, nil
}

func formatSessionState(state sessionStateRow) string {
	var lines []string
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

func formatDurableMemories(memories []durableMemoryRow) string {
	lines := make([]string, 0, len(memories))
	for _, memory := range memories {
		content := strings.TrimSpace(memory.Content)
		if content == "" {
			continue
		}
		kind := strings.TrimSpace(memory.Kind)
		if kind == "" {
			kind = "memory"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", kind, content))
	}
	return strings.Join(lines, "\n")
}
