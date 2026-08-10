package evidence

import "testing"

func TestValidateAnswerRejectsUnknownEvidenceID(t *testing.T) {
	available := EvidenceSet{
		EvidenceIDs:    map[string]bool{"ev_1_abcd1234": true},
		FailedQueryIDs: map[string]bool{},
	}
	got := ValidateAnswer("confirmed by ev_2_abcd1234", available)
	if got.OK {
		t.Fatalf("expected unknown evidence id rejection")
	}
}

func TestValidateAnswerOnlyChecksIdentifierReferences(t *testing.T) {
	available := EvidenceSet{
		EvidenceIDs:    map[string]bool{},
		FailedQueryIDs: map[string]bool{"fq_1_abcd1234": true},
	}
	got := ValidateAnswer("the metric is normal", available)
	if !got.OK {
		t.Fatalf("natural language must not control validation, got %#v", got)
	}
	got = ValidateAnswer("the check failed: fq_1_abcd1234", available)
	if !got.OK {
		t.Fatalf("expected known failed query id to pass, got %#v", got)
	}
	got = ValidateAnswer("the check failed: fq_unknown", available)
	if got.OK {
		t.Fatalf("expected unknown failed query id rejection")
	}
}

func TestValidateAnswerDoesNotUseDiagnosticKeywords(t *testing.T) {
	available := EvidenceSet{
		EvidenceIDs:    map[string]bool{"ev_1_abcd1234": true},
		FailedQueryIDs: map[string]bool{},
	}
	got := ValidateAnswer("diagnosis: Redis latency caused payment failure", available)
	if !got.OK {
		t.Fatalf("diagnostic wording must not control validation, got %#v", got)
	}
	got = ValidateAnswer("diagnosis: Redis latency ev_1_abcd1234", available)
	if !got.OK {
		t.Fatalf("expected known evidence id to pass, got %#v", got)
	}
}
