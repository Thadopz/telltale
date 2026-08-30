package eval

import (
	"strings"
	"testing"
)

func TestFilterCasesByID(t *testing.T) {
	cases := []Case{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	filtered, err := filterCasesByID(cases, []string{"three", "one"})
	if err != nil {
		t.Fatalf("filter cases: %v", err)
	}
	if len(filtered) != 2 || filtered[0].ID != "one" || filtered[1].ID != "three" {
		t.Fatalf("unexpected filtered cases: %#v", filtered)
	}
	_, err = filterCasesByID(cases, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown id should fail clearly: %v", err)
	}
}
