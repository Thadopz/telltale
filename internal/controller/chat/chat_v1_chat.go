package chat

import (
	"SREagent/api/chat/v1"
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/internal/ai/agent/trace"
	"SREagent/internal/observability"
	"SREagent/utility/log_call_back"
	"SREagent/utility/mem"
	"context"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

func (c *ControllerV1) Chat(ctx context.Context, req *v1.ChatReq) (res *v1.ChatRes, err error) {
	startedAt := time.Now()
	if httpRequest := g.RequestFromCtx(ctx); httpRequest != nil {
		ctx = observability.ExtractHTTPContext(ctx, httpRequest.Header)
	}
	ctx, span := observability.StartSpan(ctx, "chat.request", attribute.Bool("chat.stream", false))
	answerLength := 0
	var recorder *trace.Recorder
	defer func() {
		evidenceCount, failedToolCount := recorder.QualityCounts()
		observability.RecordChat(ctx, false, time.Since(startedAt), err, answerLength, evidenceCount, failedToolCount)
		observability.EndSpan(span, err,
			attribute.Int("chat.answer.characters", answerLength),
			attribute.Int("chat.evidence.items", evidenceCount),
			attribute.Int("chat.tool.failures", failedToolCount),
		)
	}()

	turn, err := prepareChatTurn(ctx, req.Id, req.UserId, req.ConversationId, req.Question, req.KnowledgeMode)
	if err != nil {
		return nil, err
	}
	ctx = turn.Context

	runner, err := chat_pipeline.BuildChatAgent(ctx)
	if err != nil {
		return nil, err
	}

	recorder = trace.NewRecorder()
	out, err := runner.Invoke(ctx, turn.UserMessage, compose.WithCallbacks(
		log_call_back.LogCallback(nil),
		trace.Callback("chat_agent", recorder),
		mem.ToolResultCallback(turn.UserID, turn.ConversationID),
	))
	if err != nil {
		return nil, err
	}
	answer, err := chat_pipeline.ConvergeAnswer(ctx, chat_pipeline.AnswerConvergeInput{
		Question:     turn.Question,
		Answer:       out.Content,
		Steps:        recorder.Steps(),
		ToolEvidence: recorder.ToolEvidence(),
	})
	if err != nil {
		return nil, err
	}
	if err = saveCompletedTurn(ctx, turn, answer); err != nil {
		return nil, err
	}
	answerLength = len([]rune(answer))

	return &v1.ChatRes{Answer: answer}, nil
}
