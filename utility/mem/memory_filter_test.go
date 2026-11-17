package mem

import "testing"

func TestMemoryFilters(t *testing.T) {
	for _, value := range []string{"API key: secret", "Bearer abcdefghijklmnop", "postgres://user:password@host/db", "-----BEGIN RSA PRIVATE KEY-----"} {
		if !containsSensitiveMemory(value) {
			t.Fatalf("sensitive memory accepted: %q", value)
		}
	}
	for _, value := range []string{"本次故障导致超时", "current error rate is high"} {
		if !containsTemporaryMemory(value) {
			t.Fatalf("temporary memory accepted: %q", value)
		}
	}
	if containsSensitiveMemory("偏好简洁回答") || containsTemporaryMemory("偏好简洁回答") {
		t.Fatal("ordinary preference rejected")
	}
}
