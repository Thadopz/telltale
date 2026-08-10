package chat

import (
	"context"
	"testing"

	v1 "github.com/Thadopz/telltale/api/chat/v1"
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

func TestChatUsesSharedDiagnosticService(t *testing.T) {
	service := &fakeDiagnosticService{result: diagnosis.DiagnoseResult{
		Answer:         "answer",
		ConversationID: "incident-1",
		EvidenceIDs:    []string{"ev-1"},
		TraceSummary:   diagnosis.TraceSummary{EvidenceCount: 1},
	}}
	controller := &ControllerV1{diagnostic: service}
	response, err := controller.Chat(context.Background(), &v1.ChatReq{
		ConversationId: "incident-1",
		Question:       "支付回调失败",
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if response == nil || response.Answer != service.result.Answer {
		t.Fatalf("response = %+v", response)
	}
	if service.request.Question != "支付回调失败" || service.request.ConversationID != "incident-1" {
		t.Fatalf("diagnostic request = %+v", service.request)
	}
}
