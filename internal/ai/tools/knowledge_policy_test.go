package tools

import "testing"

func TestKnowledgeSearchDeduplicationIncludesDocumentScope(t *testing.T) {
	state := &knowledgeSearchState{queries: map[string]bool{}}
	if err := state.beginScoped("授权规则", "current-v2"); err != nil {
		t.Fatal(err)
	}
	if err := state.beginScoped("授权规则", "history-v1"); err != nil {
		t.Fatal("historical lookup rejected as duplicate")
	}
	if err := state.beginScoped("授权规则", "history-v1"); err == nil {
		t.Fatal("same scope duplicate accepted")
	}
}
