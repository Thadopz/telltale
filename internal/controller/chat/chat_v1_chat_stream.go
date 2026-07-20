package chat

import (
	v1 "SREagent/api/chat/v1"
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/observability"
	"SREagent/utility/log_call_back"
	"SREagent/utility/mem"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

func (c *ControllerV1) ChatStream(ctx context.Context, req *v1.ChatStreamReq) (res *v1.ChatStreamRes, err error) {
	startedAt := time.Now()
	if httpRequest := g.RequestFromCtx(ctx); httpRequest != nil {
		ctx = observability.ExtractHTTPContext(ctx, httpRequest.Header)
	}
	ctx, span := observability.StartSpan(ctx, "chat.request", attribute.Bool("chat.stream", true))
	answerLength := 0
	var recorder *trace.Recorder
	defer func() {
		evidenceCount, failedToolCount := recorder.QualityCounts()
		observability.RecordChat(ctx, true, time.Since(startedAt), err, answerLength, evidenceCount, failedToolCount)
		observability.EndSpan(span, err,
			attribute.Int("chat.answer.characters", answerLength),
			attribute.Int("chat.evidence.items", evidenceCount),
			attribute.Int("chat.tool.failures", failedToolCount),
		)
	}()

	turn, err := prepareChatTurn(ctx, req.Id, req.UserId, req.ConversationId, req.Question)
	if err != nil {
		return nil, err
	}
	ctx = turn.Context

	client, err := c.service.Create(ctx, g.RequestFromCtx(ctx))
	if err != nil {
		return nil, err
	}

	runner, err := chat_pipeline.BuildChatAgent(ctx)
	if err != nil {
		client.SendToClient("error", err.Error())
		return nil, err
	}
	recorder = trace.NewRecorder()
	client.SendToClient("thinking", `{"message":"analyzing request"}`)
	sr, err := runner.Stream(ctx, turn.UserMessage, compose.WithCallbacks(
		log_call_back.LogCallback(nil),
		trace.StreamingCallback("chat_stream_agent", recorder, func(event string, step trace.AgentStep) {
			client.SendToClient(event, marshalTraceStep(step))
		}),
		mem.ToolResultCallback(turn.UserID, turn.ConversationID),
	))
	if err != nil {
		client.SendToClient("error", err.Error())
		return nil, err
	}
	defer sr.Close()

	var fullResponse strings.Builder
	firstTokenRecorded := false

	for {
		chunk, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			originalResponse := fullResponse.String()
			guarded, guardErr := chat_pipeline.ConvergeAnswer(ctx, chat_pipeline.AnswerConvergeInput{
				Question:     turn.Question,
				Answer:       originalResponse,
				Steps:        recorder.Steps(),
				ToolEvidence: recorder.ToolEvidence(),
			})
			if guardErr != nil {
				client.SendToClient("error", guardErr.Error())
				return nil, guardErr
			}
			if guarded != originalResponse {
				client.SendToClient("replace_message", marshalStreamContent(guarded))
			}
			// 只有完整生成并通过证据收敛的回答才能写入会话，避免后续对话读取半截内容。
			if saveErr := saveCompletedTurn(ctx, turn, guarded); saveErr != nil {
				client.SendToClient("error", saveErr.Error())
				return nil, saveErr
			}
			answerLength = len([]rune(guarded))
			client.SendToClient("final_message", marshalStreamContent(guarded))
			client.SendToClient("message", guarded)
			client.SendToClient("done", "Stream completed")
			return &v1.ChatStreamRes{}, nil
		}
		if err != nil {
			client.SendToClient("error", err.Error())
			return nil, err
		}
		fullResponse.WriteString(chunk.Content)
		if chunk.Content != "" {
			if !firstTokenRecorded {
				observability.RecordFirstToken(ctx, time.Since(startedAt))
				firstTokenRecorded = true
			}
			client.SendToClient("draft_message", chunk.Content)
		}
	}
}

func marshalTraceStep(step trace.AgentStep) string {
	b, err := json.Marshal(step)
	if err != nil {
		return `{"error":"marshal trace step failed"}`
	}
	return string(b)
}

func marshalStreamContent(content string) string {
	b, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return `{"content":""}`
	}
	return string(b)
}
