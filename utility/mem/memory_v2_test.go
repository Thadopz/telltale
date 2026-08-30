package mem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

func TestMain(testingMain *testing.M) {
	ctx := context.Background()
	databasePath := filepath.Join(os.TempDir(), fmt.Sprintf("telltale-memory-test-%d.db", os.Getpid()))
	_ = os.Setenv("SUPERBIZ_SQLITE_PATH", databasePath)
	if err := storage.Initialize(ctx); err != nil {
		panic(fmt.Sprintf("initialize memory test storage: %v", err))
	}
	code := testingMain.Run()
	_ = g.DB().Close(ctx)
	_ = os.Remove(databasePath)
	_ = os.Remove(databasePath + "-wal")
	_ = os.Remove(databasePath + "-shm")
	os.Exit(code)
}

func TestMemoryV2BufferAndForgetInvariants(t *testing.T) {
	ctx := context.Background()

	principal := MemoryPrincipal{TenantID: "tenant-a", UserID: "shared-user"}
	storageID := "stored-conversation"
	for _, turn := range []memoryTurn{{Role: "user", Content: "请记住我偏好简洁回答"}, {Role: "assistant", Content: "收到"}} {
		if _, err := g.DB().Model("conversation_turns").Data(g.Map{
			"conversation_id": storageID, "role": turn.Role, "content": turn.Content,
		}).Insert(); err != nil {
			t.Fatalf("insert turn: %v", err)
		}
	}
	if err := EnqueueCompletedTurn(ctx, principal, "public-conversation", storageID, true); err != nil {
		t.Fatalf("enqueue turns: %v", err)
	}
	var first memoryBuffer
	if err := g.DB().Model("memory_buffers").Where("id", memoryScopeID(principal, "public-conversation")).Scan(&first); err != nil {
		t.Fatalf("load first buffer: %v", err)
	}
	if err := EnqueueCompletedTurn(ctx, principal, "public-conversation", storageID, true); err != nil {
		t.Fatalf("repeat enqueue: %v", err)
	}
	var repeated memoryBuffer
	if err := g.DB().Model("memory_buffers").Where("id", first.ID).Scan(&repeated); err != nil {
		t.Fatalf("load repeated buffer: %v", err)
	}
	if repeated.PendingTokens != first.PendingTokens || len(decodeTurnIDs(repeated.PendingTurnIDs)) != 2 {
		t.Fatalf("duplicate enqueue changed the batch: first=%#v repeated=%#v", first, repeated)
	}

	claimed, err := claimReadyMemoryBuffer(ctx, time.Now().UTC(), first.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim explicit buffer: claimed=%#v err=%v", claimed, err)
	}
	profileResult, err := g.DB().Model("profile_slots").Data(g.Map{
		"tenant_id": principal.TenantID, "user_id": principal.UserID,
		"slot_key": "preference.answer_detail", "value_type": "enum",
		"value_json": `"concise"`, "value_text": "concise", "revision": 1,
		"status": profileStatusActive, "confirmation": "automatic_low_risk", "confidence": 1,
		"valid_from": time.Now().UTC(),
	}).Insert()
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	profileID, _ := profileResult.LastInsertId()
	if err := DeleteProfileSlot(ctx, principal, profileID, 1); err != nil {
		t.Fatalf("forget profile: %v", err)
	}
	var forgotten memoryBuffer
	if err := g.DB().Model("memory_buffers").Where("id", first.ID).Scan(&forgotten); err != nil {
		t.Fatalf("load forgotten buffer: %v", err)
	}
	if forgotten.Generation != 1 || forgotten.Status != memoryBufferProcessed || forgotten.PendingTurnIDs != "[]" || forgotten.ClaimedTurnIDs != "[]" {
		t.Fatalf("forget did not invalidate buffered source data: %#v", forgotten)
	}
	if _, err := commitMemoryExtraction(ctx, *claimed, nil, nil, nil, time.Now().UTC()); err == nil {
		t.Fatal("expected stale extraction commit to be rejected after forget")
	}
}

func TestMemoryV2CandidateConfirmationIsTenantScopedAndRevisionChecked(t *testing.T) {
	ctx := context.Background()

	now := time.Now().UTC()
	result, err := g.DB().Model("profile_candidates").Data(g.Map{
		"tenant_id": "tenant-a", "user_id": "shared-user",
		"slot_key": "environment.default_cluster", "value_type": "string",
		"value_json": `"prod-a"`, "value_text": "prod-a", "revision": 2,
		"confidence": 0.95, "status": "pending", "expires_at": now.Add(24 * time.Hour),
	}).Insert()
	if err != nil {
		t.Fatalf("insert candidate: %v", err)
	}
	candidateID, _ := result.LastInsertId()
	wrongTenant := MemoryPrincipal{TenantID: "tenant-b", UserID: "shared-user"}
	if _, err := ConfirmProfileCandidate(ctx, wrongTenant, candidateID, 2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant confirmation should be hidden, got %v", err)
	}
	owner := MemoryPrincipal{TenantID: "tenant-a", UserID: "shared-user"}
	if _, err := ConfirmProfileCandidate(ctx, owner, candidateID, 1); err == nil {
		t.Fatal("expected stale candidate revision to fail")
	}
	saved, err := ConfirmProfileCandidate(ctx, owner, candidateID, 2)
	if err != nil {
		t.Fatalf("confirm candidate: %v", err)
	}
	if saved.SlotKey != "environment.default_cluster" || saved.Confirmation != "confirmed_by_user" {
		t.Fatalf("unexpected confirmed profile: %#v", saved)
	}
}

func TestMemoryV2ExtractionProtocolAndValidation(t *testing.T) {
	valid := `{"schema_version":1,"extractor_version":"profile-event-v1","profile_candidates":[],"events":[]}`
	if _, err := parseMemoryExtraction(valid); err != nil {
		t.Fatalf("parse valid extraction: %v", err)
	}
	if _, err := parseMemoryExtraction(`{"schema_version":1,"extractor_version":"profile-event-v1","unknown":true,"profile_candidates":[],"events":[]}`); err == nil {
		t.Fatal("expected unknown extraction fields to fail closed")
	}
	secret := extractedProfileCandidate{
		SlotKey: "environment.default_cluster", ValueType: "string",
		Value: json.RawMessage(`"password=do-not-store"`), Confidence: 0.99,
	}
	if _, ok := validateExtractedProfile(secret); ok {
		t.Fatal("expected sensitive profile value to be rejected")
	}
	if !queryRequestsHistoricalEvents("我们去年讨论过什么") || queryRequestsHistoricalEvents("检查当前订单状态") {
		t.Fatal("historical query detection returned the wrong boundary")
	}
	for attempt, want := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		if got := memoryRetryDelay(attempt + 1); got != want {
			t.Fatalf("retry %d: want %s, got %s", attempt+1, want, got)
		}
	}
}

func TestTenantConversationIDSeparatesIdenticalUserAndConversation(t *testing.T) {
	conversation := "same-conversation"
	first := TenantConversationID(MemoryPrincipal{TenantID: "tenant-a", UserID: "same-user"}, conversation)
	second := TenantConversationID(MemoryPrincipal{TenantID: "tenant-b", UserID: "same-user"}, conversation)
	if first == second {
		t.Fatalf("tenant conversation storage keys collided: %q", first)
	}
	if first != TenantConversationID(MemoryPrincipal{TenantID: "tenant-a", UserID: "same-user"}, conversation) {
		t.Fatal("tenant conversation storage key must be deterministic")
	}
}
