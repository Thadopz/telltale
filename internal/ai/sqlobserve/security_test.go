package sqlobserve

import (
	"strings"
	"testing"
)

func TestGuardRawReadSQLRejectsSideEffectsAndResourceAbuse(t *testing.T) {
	tests := []string{
		"SELECT secret FROM credentials INTO OUTFILE '/tmp/leak.txt'",
		"SELECT LOAD_FILE('/etc/passwd')",
		"SELECT SLEEP(30)",
		"SELECT BENCHMARK(100000000, SHA2('x', 256))",
		"SELECT GET_LOCK('telltale-test', 30)",
		"SELECT * FROM orders FOR UPDATE",
	}

	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			if _, err := guardRawReadSQL(sql, 100); err == nil {
				t.Fatalf("expected unsafe read query to be rejected: %s", sql)
			}
		})
	}
}

func TestGuardRawReadSQLAddsTopLevelLimitWhenOnlySubqueryIsLimited(t *testing.T) {
	sql := "SELECT (SELECT id FROM tenants LIMIT 1) AS tenant_id FROM large_audit_log"
	guarded, err := guardRawReadSQL(sql, 100)
	if err != nil {
		t.Fatalf("guard raw read: %v", err)
	}

	if !strings.HasSuffix(strings.ToLower(guarded), "limit 100") {
		t.Fatalf("expected an outer result limit, got %q", guarded)
	}
}
