package server

import (
	"context"
	"testing"
)

func TestAddressHelpersUseLocalDefault(t *testing.T) {
	if got := hostFromAddress(":6872"); got != "127.0.0.1" {
		t.Fatalf("host = %q", got)
	}
	if got := portFromAddress("127.0.0.1:7000"); got != 7000 {
		t.Fatalf("port = %d", got)
	}
	if got := netAddress("127.0.0.1", 6872); got != "127.0.0.1:6872" {
		t.Fatalf("address = %q", got)
	}
}

func TestValidateListenAddressRejectsAccidentalRemoteExposure(t *testing.T) {
	if err := validateListenAddress(context.Background(), "127.0.0.1:6872"); err != nil {
		t.Fatalf("loopback address should be allowed: %v", err)
	}
	if err := validateListenAddress(context.Background(), "[::1]:6872"); err != nil {
		t.Fatalf("IPv6 loopback address should be allowed: %v", err)
	}
	if err := validateListenAddress(context.Background(), "0.0.0.0:6872"); err == nil {
		t.Fatal("expected wildcard address to require an explicit remote opt-in")
	}
}
