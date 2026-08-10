package actions

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

func TestSQLiteActionStoreRoundTrip(t *testing.T) {
	t.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(t.TempDir(), "telltale.db"))
	ctx := context.Background()
	if err := storage.Initialize(ctx); err != nil {
		t.Fatalf("initialize sqlite storage: %v", err)
	}
	defer g.DB().Close(ctx)

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	want := pendingAction{
		PreparedAction: PreparedAction{
			Token:           "sqlite-token",
			ToolName:        "mysql_crud",
			OperationID:     "op-sqlite-1",
			ArgumentsHash:   "hash",
			Risk:            "write_or_risky",
			RequiresConfirm: true,
			ExpiresAt:       expiresAt,
		},
		UserID:        "local-user",
		TenantID:      "local",
		RolesKey:      "oncall",
		ArgumentsJSON: `{ "operate_type": "update" }`,
	}

	store := &sqliteActionStore{}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("save sqlite action: %v", err)
	}
	got, err := store.Load(ctx, want.Token)
	if err != nil {
		t.Fatalf("load sqlite action: %v", err)
	}
	if got.Token != want.Token || got.ToolName != want.ToolName || got.OperationID != want.OperationID || got.ArgumentsJSON != want.ArgumentsJSON {
		t.Fatalf("unexpected loaded action: %#v", got)
	}
	if err := store.Confirm(ctx, want.Token); err != nil {
		t.Fatalf("confirm sqlite action: %v", err)
	}
	got, err = store.Load(ctx, want.Token)
	if err != nil || !got.Confirmed {
		t.Fatalf("expected confirmed sqlite action, action=%#v err=%v", got, err)
	}
	if err := store.Delete(ctx, want.Token); err != nil {
		t.Fatalf("delete sqlite action: %v", err)
	}
	if _, err := store.Load(ctx, want.Token); err != sql.ErrNoRows {
		t.Fatalf("expected deleted action to be absent, got %v", err)
	}
}
