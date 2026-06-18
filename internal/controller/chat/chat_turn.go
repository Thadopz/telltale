package chat

import (
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/internal/authz"
	"SREagent/utility/mem"
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

type preparedChatTurn struct {
	Context        context.Context
	UserID         string
	ConversationID string
	Question       string
	Memory         *mem.SimpleMemory
	UserMessage    *chat_pipeline.UserMessage
}

func prepareChatTurn(
	ctx context.Context,
	id string,
	userID string,
	conversationID string,
	question string,
	knowledgeMode string,
) (*preparedChatTurn, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}

	userID, conversationID = resolveMemoryScope(id, userID, conversationID)
	principal := authz.PrincipalFromRequest(ctx, g.RequestFromCtx(ctx), userID)
	if principal.UserID != "" {
		userID = principal.UserID
	}
	if conversationID == "" {
		conversationID = userID
	}
	if userID == "" || conversationID == "" {
		return nil, fmt.Errorf("user id or conversation id is required")
	}

	mode, err := normalizeKnowledgeMode(knowledgeMode)
	if err != nil {
		return nil, err
	}

	ctx = authz.WithPrincipal(ctx, principal)
	ctx = context.WithValue(ctx, "client_id", conversationID)

	memory := mem.GetConversationMemory(userID, conversationID)
	layers, err := mem.LoadContextLayers(ctx, userID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("load chat context: %w", err)
	}

	return &preparedChatTurn{
		Context:        ctx,
		UserID:         userID,
		ConversationID: conversationID,
		Question:       question,
		Memory:         memory,
		UserMessage: &chat_pipeline.UserMessage{
			ID:             conversationID,
			UserID:         userID,
			ConversationID: conversationID,
			Query:          question,
			History:        memory.GetContextMessages(mem.DefaultContextPolicy()),
			Summary:        memory.GetSummary(),
			SessionState:   layers.SessionState,
			DurableMemory:  layers.DurableMemory,
			ToolResults:    layers.ToolResults,
			KnowledgeMode:  mode,
		},
	}, nil
}

func normalizeKnowledgeMode(raw string) (chat_pipeline.KnowledgeMode, error) {
	switch chat_pipeline.KnowledgeMode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", chat_pipeline.KnowledgeAuto:
		return chat_pipeline.KnowledgeAuto, nil
	case chat_pipeline.KnowledgeAlways:
		return chat_pipeline.KnowledgeAlways, nil
	case chat_pipeline.KnowledgeNever:
		return chat_pipeline.KnowledgeNever, nil
	default:
		return "", fmt.Errorf("knowledge mode must be auto, always, or never")
	}
}

func saveCompletedTurn(ctx context.Context, turn *preparedChatTurn, answer string) error {
	appendCompletedMessages(turn.Memory, turn.Question, answer)

	if err := mem.UpdateSessionState(ctx, turn.UserID, turn.ConversationID, turn.Question, answer); err != nil {
		return fmt.Errorf("update chat session state: %w", err)
	}
	return nil
}

func appendCompletedMessages(memory *mem.SimpleMemory, question string, answer string) {
	memory.SetMessages(schema.UserMessage(question))
	memory.SetMessages(schema.AssistantMessage(answer, nil))
}
