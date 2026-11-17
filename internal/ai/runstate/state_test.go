package runstate

import "testing"

func TestStateIsolationAndNestedSharing(t *testing.T) {
	first := WithState(t.Context())
	nested := WithState(first)
	second := WithState(t.Context())
	Cache(first, "query", "original result")
	if result, ok := Cached(nested, "query"); !ok || result != "original result" {
		t.Fatal("nested calls lost the retained result")
	}
	if _, ok := Cached(second, "query"); ok {
		t.Fatal("query result leaked across turns")
	}
	Complete(nested)
	if !Completed(first) || Completed(second) {
		t.Fatal("completion state did not respect turn boundaries")
	}
}
