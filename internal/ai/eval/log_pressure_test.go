package eval

import "testing"

func TestPressureMarkersDoNotRejectEvidenceAttestation(t *testing.T) {
	fixtures := []ToolFixture{{Name: "db_slow_queries", Output: `{"attestation":{},"summary":"latest observation"}`}}
	if hits := contextStressToolMarkerHits(fixtures); len(hits) > 0 {
		t.Fatalf("protocol fields mistaken for markers: %v", hits)
	}
	fixtures[0].Description = "fake_tool for a test"
	if hits := contextStressToolMarkerHits(fixtures); len(hits) != 2 {
		t.Fatalf("real marker words not caught: %v", hits)
	}
}
