package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSQLSnapshotCommandsRejectHistoryFlags(t *testing.T) {
	var output bytes.Buffer
	err := runSQL(context.Background(), []string{"active-queries", "--last", "30m"}, "", &output)
	if err == nil || !strings.Contains(err.Error(), "only valid for slow-sql") {
		t.Fatalf("expected snapshot time flag error, got %v", err)
	}
}

func TestSQLSlowQueryRejectsConflictingWindowAliases(t *testing.T) {
	var output bytes.Buffer
	err := runSQL(context.Background(), []string{"slow-sql", "--last", "30m", "--window", "1h"}, "", &output)
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("expected conflicting window alias error, got %v", err)
	}
}
