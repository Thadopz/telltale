package documentpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestDeclaredMetadataValidation(t *testing.T) {
	for _, raw := range []string{
		`{"state":"active"}`, `{"state":"current"}`, `{"state":"unverified","verified_at":"yesterday"}`,
		`{"state":"unverified","valid_from":"2026-11-04T00:00:00Z","valid_until":"2026-11-04T00:00:00Z"}`,
		`{"state":"current","version":"v2","verified_by":"reviewer","verified_at":"2026-11-04T00:00:00Z","superseded_by":"v3.md"}`,
		`{"state":"unverified","unknown":"field"}`, `{} {}`,
	} {
		if _, err := Decode(strings.NewReader(raw)); err == nil {
			t.Fatalf("invalid metadata accepted: %s", raw)
		}
	}
	if record, err := FromMetadata(nil); err != nil || record.State != "unverified" {
		t.Fatal("legacy documents inferred current")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", 65537))); err == nil {
		t.Fatal("unbounded sidecar accepted")
	}
}

func TestValidityBoundariesAndHistoricalVersionScope(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	current, _ := (Options{}).Resolve(now)
	history, _ := (Options{Mode: "history"}).Resolve(now)
	for _, state := range []string{"planned", "deprecated"} {
		r := Record{State: state}
		if r.Allows(current) || !r.Allows(history) {
			t.Fatalf("wrong scope for %s", state)
		}
	}
	r := Record{State: "unverified", Version: "v2", ValidFrom: now.Format(time.RFC3339Nano), ValidUntil: now.Add(time.Nanosecond).Format(time.RFC3339Nano)}
	if !r.Allows(current) {
		t.Fatal("valid_from boundary excluded")
	}
	current.At = now.Add(time.Nanosecond)
	if r.Allows(current) {
		t.Fatal("valid_until equality accepted")
	}
	if !r.Allows(history) {
		t.Fatal("history without time incorrectly excluded expired source")
	}
	fixed, _ := (Options{Mode: "history", AsOf: now.Add(time.Nanosecond).Format(time.RFC3339Nano)}).Resolve(now)
	if r.Allows(fixed) {
		t.Fatal("historical as_of ignored validity")
	}
	current.At = now
	current.Version = "v3"
	if r.Allows(current) {
		t.Fatal("version mismatch accepted")
	}
	r = Record{State: "unverified", SupersededBy: "replacement.md"}
	current.Version = ""
	if r.Allows(current) || !r.Allows(history) {
		t.Fatal("replacement boundary ignored")
	}
	if value := (Record{ValidFrom: now.Add(time.Second).Format(time.RFC3339Nano)}).NextChange(now); !value.Equal(now.Add(time.Second)) {
		t.Fatal("future activation boundary missing")
	}
}

func TestScopeCacheKeysAreDistinctAndCanonical(t *testing.T) {
	now := time.Now()
	a, _ := (Options{Mode: "history", Version: "V2", AsOf: "2026-11-04T08:00:00+08:00"}).Resolve(now)
	b, _ := (Options{Mode: "history", Version: "V2", AsOf: "2026-11-04T00:00:00Z"}).Resolve(now)
	if a.CacheKey() != b.CacheKey() {
		t.Fatal("equivalent timestamps diverged")
	}
	c, _ := (Options{Mode: "history", Version: "v2", AsOf: b.AsOf}).Resolve(now)
	if a.CacheKey() == c.CacheKey() {
		t.Fatal("case-sensitive versions shared a cache key")
	}
	for _, options := range []Options{{Mode: "everything"}, {AsOf: "2026-11-04"}} {
		if _, err := options.Resolve(now); err == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}
