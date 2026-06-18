package main

import "testing"

func TestParseServerPort(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    int
	}{
		{name: "plain port", address: "6872", want: 6872},
		{name: "colon port", address: ":6872", want: 6872},
		{name: "host port", address: "127.0.0.1:6873", want: 6873},
		{name: "empty fallback", address: "", want: defaultServerPort},
		{name: "invalid fallback", address: "not-a-port", want: defaultServerPort},
		{name: "out of range fallback", address: ":70000", want: defaultServerPort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseServerPort(tt.address, defaultServerPort); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}
