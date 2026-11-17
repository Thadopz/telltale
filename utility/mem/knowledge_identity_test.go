package mem

import (
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/gogf/gf/v2/frame/g"
	"strings"
	"testing"
)

func TestKnowledgeHashesRemainReferencesWhileSecretsAreRedacted(t *testing.T) {
	hash := strings.Repeat("abc012ef", 8)
	data := map[string]any{"document_id": hash, "metadata": map[string]any{"_source_hash": hash}, "api_key": hash, "untrusted_text": hash}
	redacted := RedactKnowledgeFields(data)
	if redacted["document_id"] != hash || redacted["metadata"].(map[string]any)["_source_hash"] != hash {
		t.Fatal("knowledge references lost")
	}
	if redacted["api_key"] == hash || redacted["untrusted_text"] == hash {
		t.Fatal("secret redaction bypassed")
	}
	if RedactToolPayload(map[string]any{"document_id": hash}).(map[string]any)["document_id"] == hash {
		t.Fatal("generic tools bypassed redaction")
	}
}

func TestOriginalKnowledgeAuditIsStoredOutsideModelMemory(t *testing.T) {
	ctx := t.Context()
	principal := MemoryPrincipal{TenantID: "audit-tenant", UserID: "audit-user"}
	scope := normalizeMemoryScope(principal.UserID, TenantConversationID(principal, t.Name()))
	text := strings.Repeat("原始说明与否定条件。", 500)
	env := evidence.NewDataWithObservations(nil, "original", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": strings.Repeat("ab", 32), "runbook_rule": text}}})
	if err := SaveKnowledgeAudit(ctx, principal, t.Name(), "{}", evidence.Marshal(env)); err != nil {
		t.Fatal(err)
	}
	value, err := g.DB().Model("tool_results").Ctx(ctx).Where("conversation_id", scope.StorageID).Value("output")
	if err != nil {
		t.Fatal(err)
	}
	stored := evidence.Parse(value.String()).Envelope
	if stored.Evidence == nil || stored.Evidence.Observations[0].Fields["runbook_rule"] != text {
		t.Fatal("original audit truncated")
	}
	items, err := ListUsableEvidence(ctx, scope.StorageID, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("audit became claim evidence: %v", err)
	}
	recent, err := loadRecentToolResults(ctx, scope.StorageID)
	if err != nil || recent != "" {
		t.Fatal("raw audit entered model memory")
	}
}

func TestKnowledgeAuditTenantIsolationAndNormalConversationLookup(t *testing.T) {
	ctx := t.Context()
	conversation := t.Name()
	user := "same-user"
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		principal := MemoryPrincipal{TenantID: tenant, UserID: user}
		env := evidence.NewDataWithObservations(nil, "original", []evidence.Observation{{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": tenant, "runbook_rule": "authorized for " + tenant}}})
		if err := SaveKnowledgeAudit(ctx, principal, conversation, "{}", evidence.Marshal(env)); err != nil {
			t.Fatal(err)
		}
		storageID := TenantConversationID(principal, conversation)
		// A normal callback and its audit must resolve to the very same stored scope.
		if err := SaveToolResult(ctx, user, storageID, ToolResult{ToolName: "normal_tool", OutputSummary: tenant, Status: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		storageID := normalizeMemoryScope(user, TenantConversationID(MemoryPrincipal{TenantID: tenant, UserID: user}, conversation)).StorageID
		var rows []struct {
			ToolName string `json:"tool_name"`
			Output   string `json:"output"`
		}
		if err := g.DB().Model("tool_results").Ctx(ctx).Where("conversation_id", storageID).Fields("tool_name", "output").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("%s cannot locate normal and audit records: %d", tenant, len(rows))
		}
		for _, row := range rows {
			if row.ToolName == "query_internal_docs.audit" {
				env := evidence.Parse(row.Output).Envelope
				if env.Evidence.Observations[0].Fields["document_id"] != tenant {
					t.Fatal("cross-tenant audit exposure")
				}
			}
		}
	}
	legacy := normalizeMemoryScope(user, conversation).StorageID
	count, err := g.DB().Model("tool_results").Ctx(ctx).Where("conversation_id", legacy).Count()
	if err != nil || count != 0 {
		t.Fatalf("audit written to unscoped session: %d %v", count, err)
	}
}
