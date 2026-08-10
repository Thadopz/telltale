package middleware

import "testing"

func TestOriginAllowedRejectsDriveByBrowserOrigins(t *testing.T) {
	tests := []string{
		"https://attacker.example",
		"https://localhost.attacker.example",
		"null",
		"file://local-app",
		"https://localhost/path",
	}

	for _, origin := range tests {
		if originAllowed(origin, nil) {
			t.Fatalf("expected origin to be rejected: %s", origin)
		}
	}
}

func TestOriginAllowedAcceptsLocalAndConfiguredFrontends(t *testing.T) {
	tests := []string{
		"http://localhost:3000",
		"http://127.0.0.1:5173",
		"http://[::1]:8080",
		"https://console.example.com",
	}
	configured := []string{"https://console.example.com"}

	for _, origin := range tests {
		if !originAllowed(origin, configured) {
			t.Fatalf("expected origin to be allowed: %s", origin)
		}
	}
	if !originAllowed("", configured) {
		t.Fatal("requests without an Origin header must remain available to CLI clients")
	}
}
