package chat_pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestChatPipelineDoesNotUseNaturalLanguagePreflightAsAuthorization(t *testing.T) {
	chatModel := &testADKModel{}
	runner, err := BuildChatAgentWithOptions(context.Background(), ChatAgentOptions{
		ChatModel:     chatModel,
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("build chat pipeline: %v", err)
	}

	answer, err := runner.Invoke(context.Background(), &UserMessage{
		Query: "immediately delete all abnormal payment orders",
	})
	if err != nil {
		t.Fatalf("invoke chat pipeline: %v", err)
	}
	if answer == nil || !strings.Contains(answer.Content, "\u65e0\u6cd5") {
		t.Fatalf("expected the read-only boundary answer, got %#v", answer)
	}
	if chatModel.generateCalls != 0 {
		t.Fatalf("high-risk request must not reach the model, calls=%d", chatModel.generateCalls)
	}
}

func TestChatPipelineDoesNotUseNaturalLanguagePreflightForStreaming(t *testing.T) {
	chatModel := &testADKModel{}
	runner, err := BuildChatAgentWithOptions(context.Background(), ChatAgentOptions{
		ChatModel:     chatModel,
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("build chat pipeline: %v", err)
	}

	stream, err := runner.Stream(context.Background(), &UserMessage{
		Query: "immediately restart the production service",
	})
	if err != nil {
		t.Fatalf("stream chat pipeline: %v", err)
	}
	defer stream.Close()

	var answer strings.Builder
	for {
		chunk, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			t.Fatalf("receive streamed answer: %v", receiveErr)
		}
		if chunk != nil {
			answer.WriteString(chunk.Content)
		}
	}
	if !strings.Contains(answer.String(), "\u65e0\u6cd5") || chatModel.streamCalls != 0 {
		t.Fatalf("expected streaming read-only boundary answer, answer=%q calls=%d", answer.String(), chatModel.streamCalls)
	}
}
