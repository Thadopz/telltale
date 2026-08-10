package chat

import (
	"context"
	"time"

	"github.com/Thadopz/telltale/api/chat/v1"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

// Chat is the HTTP adapter for the shared diagnostic service.
func (c *ControllerV1) Chat(ctx context.Context, req *v1.ChatReq) (res *v1.ChatRes, err error) {
	startedAt := time.Now()
	if httpRequest := g.RequestFromCtx(ctx); httpRequest != nil {
		ctx = observability.ExtractHTTPContext(ctx, httpRequest.Header)
	}
	ctx, span := observability.StartSpan(ctx, "chat.request", attribute.Bool("chat.stream", false))
	answerLength := 0
	evidenceCount := 0
	failedToolCount := 0
	defer func() {
		observability.RecordChat(ctx, false, time.Since(startedAt), err, answerLength, evidenceCount, failedToolCount)
		observability.EndSpan(span, err,
			attribute.Int("chat.answer.characters", answerLength),
			attribute.Int("chat.evidence.items", evidenceCount),
			attribute.Int("chat.tool.failures", failedToolCount),
		)
	}()

	_, conversationID := resolveMemoryScope(req.Id, "", req.ConversationId)
	if conversationID == "" {
		conversationID = authz.ConfiguredPrincipal(ctx).UserID
	}
	result, err := c.diagnostic.Diagnose(ctx, diagnosis.DiagnoseRequest{
		Question:       req.Question,
		ConversationID: conversationID,
	})
	if err != nil {
		return nil, err
	}

	evidenceCount = result.TraceSummary.EvidenceCount
	failedToolCount = result.TraceSummary.FailedToolCount
	answerLength = len([]rune(result.Answer))
	return &v1.ChatRes{Answer: result.Answer}, nil
}
