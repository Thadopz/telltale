package chat

import (
	"SREagent/api/chat/v1"
	"SREagent/internal/authz"
	"SREagent/utility/mem"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

func (c *ControllerV1) MemoryList(ctx context.Context, req *v1.MemoryListReq) (*v1.MemoryListRes, error) {
	userID, err := memoryAPIUserID(ctx)
	if err != nil {
		return nil, err
	}

	memories, err := mem.ListDurableMemories(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]v1.MemoryItem, 0, len(memories))
	for _, memory := range memories {
		items = append(items, memoryItem(memory))
	}
	return &v1.MemoryListRes{Items: items}, nil
}

func (c *ControllerV1) MemoryDelete(ctx context.Context, req *v1.MemoryDeleteReq) (*v1.MemoryDeleteRes, error) {
	userID, err := memoryAPIUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req.MemoryId <= 0 {
		return nil, fmt.Errorf("memory id is required")
	}
	if err = mem.DeleteDurableMemory(ctx, userID, req.MemoryId); err != nil {
		return nil, err
	}
	return &v1.MemoryDeleteRes{}, nil
}

func (c *ControllerV1) MemoryCandidateList(ctx context.Context, req *v1.MemoryCandidateListReq) (*v1.MemoryCandidateListRes, error) {
	userID, err := memoryAPIUserID(ctx)
	if err != nil {
		return nil, err
	}

	candidates, err := mem.ListDurableMemoryCandidates(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]v1.MemoryCandidateItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, memoryCandidateItem(candidate))
	}
	return &v1.MemoryCandidateListRes{Items: items}, nil
}

func (c *ControllerV1) MemoryCandidateConfirm(ctx context.Context, req *v1.MemoryCandidateConfirmReq) (*v1.MemoryCandidateConfirmRes, error) {
	userID, err := memoryAPIUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req.CandidateId <= 0 {
		return nil, fmt.Errorf("candidate id is required")
	}

	memory, err := mem.ConfirmDurableMemoryCandidate(ctx, userID, req.CandidateId)
	if err != nil {
		return nil, err
	}
	return &v1.MemoryCandidateConfirmRes{Memory: memoryItem(memory)}, nil
}

func (c *ControllerV1) MemoryCandidateReject(ctx context.Context, req *v1.MemoryCandidateRejectReq) (*v1.MemoryCandidateRejectRes, error) {
	userID, err := memoryAPIUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req.CandidateId <= 0 {
		return nil, fmt.Errorf("candidate id is required")
	}
	if err = mem.RejectDurableMemoryCandidate(ctx, userID, req.CandidateId); err != nil {
		return nil, err
	}
	return &v1.MemoryCandidateRejectRes{}, nil
}

func memoryAPIUserID(ctx context.Context) (string, error) {
	principal := authz.PrincipalFromRequest(ctx, g.RequestFromCtx(ctx), "")
	userID := strings.TrimSpace(principal.UserID)
	if userID == "" {
		return "", fmt.Errorf("X-User-Id header is required")
	}
	return userID, nil
}

func memoryItem(memory mem.DurableMemory) v1.MemoryItem {
	return v1.MemoryItem{
		ID:                   memory.ID,
		MemoryKey:            memory.MemoryKey,
		Kind:                 memory.Kind,
		Content:              memory.Content,
		Confidence:           memory.Confidence,
		Status:               memory.Status,
		VectorStatus:         memory.VectorStatus,
		SourceConversationID: memory.SourceConversationID,
		CreatedAt:            formatMemoryTime(memory.CreatedAt),
		UpdatedAt:            formatMemoryTime(memory.UpdatedAt),
	}
}

func memoryCandidateItem(candidate mem.DurableMemoryCandidate) v1.MemoryCandidateItem {
	return v1.MemoryCandidateItem{
		ID:                   candidate.ID,
		MemoryKey:            candidate.MemoryKey,
		Kind:                 candidate.Kind,
		Content:              candidate.Content,
		Confidence:           candidate.Confidence,
		SourceConversationID: candidate.SourceConversationID,
		CreatedAt:            formatMemoryTime(candidate.CreatedAt),
		UpdatedAt:            formatMemoryTime(candidate.UpdatedAt),
	}
}

func formatMemoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}
