package lexical

import (
	"slices"
	"strings"
	"testing"
)

func TestTermsPreserveIdentifiersAndChinesePairs(t *testing.T) {
	terms := Terms("ERR_CARD_409 好友申请 /api/v1/card")
	for _, want := range []string{"err_card_409", "好友", "友申", "申请", "api", "v1", "card"} {
		if !slices.Contains(terms, want) {
			t.Fatalf("missing %q in %v", want, terms)
		}
	}
	if got := Match(`ERR_CARD_409" OR NOT (好友申请)*`); strings.Contains(got, `*`) || strings.Contains(got, `(`) || !strings.Contains(got, `"or"`) {
		t.Fatalf("unsafe MATCH expression: %s", got)
	}
	if got := Match("  !!! "); got != "" {
		t.Fatalf("punctuation generated terms: %s", got)
	}
}
