package chat

import (
	"SREagent/api/chat/v1"
	"SREagent/internal/ai/agent/chat_pipeline"
	"SREagent/internal/ai/agent/trace"
	"SREagent/utility/log_call_back"
	"SREagent/utility/mem"
	"context"

	"github.com/cloudwego/eino/compose"
)

func (c *ControllerV1) Chat(ctx context.Context, req *v1.ChatReq) (res *v1.ChatRes, err error) {
	turn, err := prepareChatTurn(ctx, req.Id, req.UserId, req.ConversationId, req.Question, req.KnowledgeMode)
	if err != nil {
		return nil, err
	}
	ctx = turn.Context

	runner, err := chat_pipeline.BuildChatAgent(ctx)
	if err != nil {
		return nil, err
	}

	recorder := trace.NewRecorder()
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

	return &v1.ChatRes{Answer: answer}, nil
}
