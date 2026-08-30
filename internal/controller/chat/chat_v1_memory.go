package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Thadopz/telltale/api/chat/v1"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/utility/mem"
	"time"
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
	userID := authz.ConfiguredPrincipal(ctx).UserID
	if userID == "" {
		return "", fmt.Errorf("identity.user_id is required")
	}
	return userID, nil
}

func memoryAPIPrincipal(ctx context.Context) (mem.MemoryPrincipal, error) {
	principal := authz.ConfiguredPrincipal(ctx)
	memoryPrincipal := mem.MemoryPrincipal{TenantID: principal.TenantID, UserID: principal.UserID}
	return memoryPrincipal, memoryPrincipal.Validate()
}

func (c *ControllerV1) ProfileList(ctx context.Context, req *v1.ProfileListReq) (*v1.ProfileListRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	profiles, err := mem.ListProfileSlots(ctx, principal, req.Limit)
	if err != nil {
		return nil, err
	}
	items := make([]v1.ProfileItem, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, profileItem(profile))
	}
	return &v1.ProfileListRes{Items: items}, nil
}

func (c *ControllerV1) ProfileDelete(ctx context.Context, req *v1.ProfileDeleteReq) (*v1.ProfileDeleteRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err = mem.DeleteProfileSlot(ctx, principal, req.ProfileId, req.Revision); err != nil {
		return nil, err
	}
	return &v1.ProfileDeleteRes{}, nil
}

func (c *ControllerV1) ProfileCandidateList(ctx context.Context, req *v1.ProfileCandidateListReq) (*v1.ProfileCandidateListRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := mem.ListProfileCandidates(ctx, principal, req.Limit)
	if err != nil {
		return nil, err
	}
	items := make([]v1.ProfileCandidateItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, profileCandidateItem(candidate))
	}
	return &v1.ProfileCandidateListRes{Items: items}, nil
}

func (c *ControllerV1) ProfileCandidateConfirm(ctx context.Context, req *v1.ProfileCandidateConfirmReq) (*v1.ProfileCandidateConfirmRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := mem.ConfirmProfileCandidate(ctx, principal, req.CandidateId, req.Revision)
	if err != nil {
		return nil, err
	}
	return &v1.ProfileCandidateConfirmRes{Profile: profileItem(profile)}, nil
}

func (c *ControllerV1) ProfileCandidateReject(ctx context.Context, req *v1.ProfileCandidateRejectReq) (*v1.ProfileCandidateRejectRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err = mem.RejectProfileCandidate(ctx, principal, req.CandidateId, req.Revision); err != nil {
		return nil, err
	}
	return &v1.ProfileCandidateRejectRes{}, nil
}

func (c *ControllerV1) EventList(ctx context.Context, req *v1.EventListReq) (*v1.EventListRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	events, err := mem.ListMemoryEvents(ctx, principal, req.Limit)
	if err != nil {
		return nil, err
	}
	items := make([]v1.EventItem, 0, len(events))
	for _, event := range events {
		items = append(items, eventItem(event))
	}
	return &v1.EventListRes{Items: items}, nil
}

func (c *ControllerV1) EventDelete(ctx context.Context, req *v1.EventDeleteReq) (*v1.EventDeleteRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err = mem.DeleteMemoryEvent(ctx, principal, req.EventId); err != nil {
		return nil, err
	}
	return &v1.EventDeleteRes{}, nil
}

func (c *ControllerV1) MemoryBufferFlush(ctx context.Context, req *v1.MemoryBufferFlushReq) (*v1.MemoryBufferFlushRes, error) {
	principal, err := memoryAPIPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err = mem.NewMemoryWorker().FlushConversation(ctx, principal, req.ConversationId); err != nil {
		return nil, err
	}
	return &v1.MemoryBufferFlushRes{Status: "flushed"}, nil
}

func profileItem(profile mem.ProfileSlot) v1.ProfileItem {
	item := v1.ProfileItem{
		ID: profile.ID, SlotKey: profile.SlotKey, ValueType: profile.ValueType,
		ValueJSON: profile.ValueJSON, ValueText: profile.ValueText,
		Revision: profile.Revision, Status: profile.Status, Confirmation: profile.Confirmation,
		Confidence: profile.Confidence, SourceConversationID: profile.SourceConversationID,
		ValidFrom: formatMemoryTime(profile.ValidFrom), UpdatedAt: formatMemoryTime(profile.UpdatedAt),
	}
	if profile.ValidUntil != nil {
		item.ValidUntil = formatMemoryTime(*profile.ValidUntil)
	}
	return item
}

func profileCandidateItem(candidate mem.ProfileCandidate) v1.ProfileCandidateItem {
	return v1.ProfileCandidateItem{
		ID: candidate.ID, SlotKey: candidate.SlotKey, ValueType: candidate.ValueType,
		ValueJSON: candidate.ValueJSON, ValueText: candidate.ValueText,
		Revision: candidate.Revision, Confidence: candidate.Confidence,
		SourceConversationID: candidate.SourceConversationID,
		ExpiresAt:            formatMemoryTime(candidate.ExpiresAt), UpdatedAt: formatMemoryTime(candidate.UpdatedAt),
	}
}

func eventItem(event mem.MemoryEvent) v1.EventItem {
	item := v1.EventItem{
		ID: event.ID, ConversationID: event.ConversationID, Summary: event.Summary,
		RecordedAt: formatMemoryTime(event.RecordedAt), TimeConfidence: event.TimeConfidence,
		EvidenceStatus: event.EvidenceStatus, Status: event.Status,
	}
	_ = json.Unmarshal([]byte(event.TagsJSON), &item.Tags)
	_ = json.Unmarshal([]byte(event.EntitiesJSON), &item.Entities)
	_ = json.Unmarshal([]byte(event.EvidenceIDs), &item.EvidenceIDs)
	if event.OccurredAt != nil {
		item.OccurredAt = formatMemoryTime(*event.OccurredAt)
	}
	return item
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
