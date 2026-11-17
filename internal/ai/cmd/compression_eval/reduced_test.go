package main

import (
	"database/sql"
	"testing"
)

func TestReducedBudgetEnforcesPersistentLimits(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec("CREATE TABLE requests (purpose TEXT UNIQUE, reserved_input INTEGER, reserved_output INTEGER, status TEXT, actual_input INTEGER, actual_output INTEGER)")
	if err != nil {
		t.Fatal(err)
	}
	if err := reducedReserve(t.Context(), db, "first", 100, 100, false); err != nil {
		t.Fatal(err)
	}
	if err := reducedReserve(t.Context(), db, "too-large", 120000, 1, false); err == nil {
		t.Fatal("accepted input beyond budget")
	}
	if err := reducedReserve(t.Context(), db, "first", 1, 1, false); err == nil {
		t.Fatal("accepted repeated request")
	}
	if err := reducedSettle(t.Context(), db, "first", Usage{Input: 20, Output: 10, Available: true}, "completed"); err != nil {
		t.Fatal(err)
	}
	var input, output int
	if err := db.QueryRow("SELECT reserved_input,reserved_output FROM requests WHERE purpose='first'").Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 20 || output != 10 {
		t.Fatalf("unexpected settled usage %d %d", input, output)
	}
	if err := reducedSettle(t.Context(), db, "first", Usage{Input: 21, Output: 10, Available: true}, "completed"); err == nil {
		t.Fatal("accepted usage above reservation")
	}
	if err := reducedReserve(t.Context(), db, "continued", 120000, 12000, true); err != nil {
		t.Fatalf("continuation could not exceed the prior aggregate limit: %v", err)
	}
}
