package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/diagnosis"
)

type fakeDiagnosticService struct {
	request diagnosis.DiagnoseRequest
	result  diagnosis.DiagnoseResult
}

func (f *fakeDiagnosticService) Diagnose(_ context.Context, request diagnosis.DiagnoseRequest) (diagnosis.DiagnoseResult, error) {
	f.request = request
	return f.result, nil
}

func (f *fakeDiagnosticService) DiagnoseStream(_ context.Context, request diagnosis.DiagnoseRequest, _ diagnosis.EventSink) (diagnosis.DiagnoseResult, error) {
	f.request = request
	return f.result, nil
}

func TestRunDiagnoseWithServiceHumanOutputIsAnswerOnly(t *testing.T) {
	service := &fakeDiagnosticService{result: diagnosis.DiagnoseResult{
		Answer:         "影响范围有限，等待进一步证据。",
		RunID:          "run-1",
		ConversationID: "incident-1",
		EvidenceIDs:    []string{"ev-1"},
		TraceSummary: diagnosis.TraceSummary{
			ToolCalls: []diagnosis.ToolCallSummary{{ToolName: "payment_agent", Summary: "bounded summary"}},
		},
	}}
	var output bytes.Buffer
	if err := RunDiagnoseWithService(context.Background(), []string{"--conversation-id", "incident-1", "支付回调失败"}, service, &output); err != nil {
		t.Fatalf("run diagnose: %v", err)
	}
	if output.String() != service.result.Answer+"\n" {
		t.Fatalf("unexpected human output: %q", output.String())
	}
	if service.request.ConversationID != "incident-1" {
		t.Fatalf("conversation id = %q", service.request.ConversationID)
	}
	if bytes.Contains(output.Bytes(), []byte("bounded summary")) || bytes.Contains(output.Bytes(), []byte("ev-1")) {
		t.Fatalf("human output leaked structured trace data: %q", output.String())
	}
}

func TestRunDiagnoseWithServiceJSONContainsStructuredResult(t *testing.T) {
	service := &fakeDiagnosticService{result: diagnosis.DiagnoseResult{
		Answer:      "answer",
		RunID:       "run-1",
		EvidenceIDs: []string{"ev-1"},
		ClaimSet:    evidence.ClaimSet{SchemaVersion: 1},
	}}
	var output bytes.Buffer
	if err := RunDiagnoseWithService(context.Background(), []string{"--json", "问题"}, service, &output); err != nil {
		t.Fatalf("run diagnose JSON: %v", err)
	}
	var decoded diagnosis.DiagnoseResult
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v; output=%s", err, output.String())
	}
	if decoded.RunID != "run-1" || len(decoded.EvidenceIDs) != 1 || decoded.ClaimSet.SchemaVersion != 1 {
		t.Fatalf("unexpected result: %+v", decoded)
	}
}

func TestSplitConfigDirCanAppearBeforeOrAfterCommand(t *testing.T) {
	dir, args, err := splitConfigDir([]string{"diagnose", "--config-dir", "config", "问题"})
	if err != nil {
		t.Fatalf("split config dir: %v", err)
	}
	if dir != "config" || len(args) != 2 || args[0] != "diagnose" || args[1] != "问题" {
		t.Fatalf("dir=%q args=%v", dir, args)
	}
}
