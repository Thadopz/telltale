package actions

import (
	"SREagent/internal/authz"
	"context"
	"strings"
	"testing"
	"time"
)

func TestPrepareRejectsWhenServerWritePolicyDisabled(t *testing.T) {
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "admin", Roles: []string{"admin"}})
	_, err := Prepare(ctx, "mysql_crud", `{"operate_type":"update","sql":"update orders set status='done'"}`)
	if err == nil || !strings.Contains(err.Error(), "server write policy disabled") {
		t.Fatalf("expected server write policy denial, got %v", err)
	}
}

func TestConfirmRequiresMatchingHashAndPrincipal(t *testing.T) {
	resetActionsForTest()
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "admin", Roles: []string{"admin"}})
	args := `{"operate_type":"update","sql":"update orders set status='done'"}`
	token := "token-confirm"
	store.Lock()
	store.actions[token] = pendingAction{
		PreparedAction: PreparedAction{
			Token:         token,
			ToolName:      "mysql_crud",
			ArgumentsHash: authz.HashArguments(args),
			ExpiresAt:     time.Now().Add(time.Minute),
		},
		UserID:        "admin",
		TenantID:      "default",
		RolesKey:      "admin",
		ArgumentsJSON: args,
	}
	store.Unlock()

	if _, err := Confirm(ctx, token, "wrong-hash"); err == nil {
		t.Fatal("expected hash mismatch to be rejected")
	}
	confirmed, err := Confirm(ctx, token, authz.HashArguments(args))
	if err != nil {
		t.Fatalf("expected confirmation success, got %v", err)
	}
	if confirmed.Status != "confirmed" {
		t.Fatalf("expected confirmed status, got %q", confirmed.Status)
	}
}

func TestExecuteRejectsUnconfirmedToken(t *testing.T) {
	resetActionsForTest()
	ctx := authz.WithPrincipal(context.Background(), authz.Principal{UserID: "admin", Roles: []string{"admin"}})
	args := `{"operate_type":"update","sql":"update orders set status='done'"}`
	token := "token-execute"
	store.Lock()
	store.actions[token] = pendingAction{
		PreparedAction: PreparedAction{
			Token:         token,
			ToolName:      "mysql_crud",
			ArgumentsHash: authz.HashArguments(args),
			ExpiresAt:     time.Now().Add(time.Minute),
		},
		UserID:        "admin",
		TenantID:      "default",
		RolesKey:      "admin",
		ArgumentsJSON: args,
		Confirmed:     false,
	}
	store.Unlock()

	if _, err := Execute(ctx, token, "mysql_crud", args); err == nil || !strings.Contains(err.Error(), "not been confirmed") {
		t.Fatalf("expected unconfirmed token rejection, got %v", err)
	}
}

func resetActionsForTest() {
	store.Lock()
	defer store.Unlock()
	store.actions = make(map[string]pendingAction)
}
