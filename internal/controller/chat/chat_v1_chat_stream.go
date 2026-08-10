package chat

import (
	"context"
	"time"

	v1 "github.com/Thadopz/telltale/api/chat/v1"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

// ChatStream maps the shared service events to the existing SSE protocol.
func (c *ControllerV1) ChatStream(ctx context.Context, req *v1.ChatStreamReq) (res *v1.ChatStreamRes, err error) {
	startedAt := time.Now()
	if httpRequest := g.RequestFromCtx(ctx); httpRequest != nil {
		ctx = observability.ExtractHTTPContext(ctx, httpRequest.Header)
	}
	ctx, span := observability.StartSpan(ctx, "chat.request", attribute.Bool("chat.stream", true))
	answerLength := 0
	evidenceCount := 0
	failedToolCount := 0
	defer func() {
		observability.RecordChat(ctx, true, time.Since(startedAt), err, answerLength, evidenceCount, failedToolCount)
		observability.EndSpan(span, err,
			attribute.Int("chat.answer.characters", answerLength),
			attribute.Int("chat.evidence.items", evidenceCount),
			attribute.Int("chat.tool.failures", failedToolCount),
		)
	}()

	client, err := c.service.Create(ctx, g.RequestFromCtx(ctx))
	if err != nil {
		return nil, err
	}

	_, conversationID := resolveMemoryScope(req.Id, "", req.ConversationId)
	if conversationID == "" {
		conversationID = authz.ConfiguredPrincipal(ctx).UserID
	}
	result, err := c.diagnostic.DiagnoseStream(ctx, diagnosis.DiagnoseRequest{
		Question:       req.Question,
		ConversationID: conversationID,
	}, func(event diagnosis.StreamEvent) error {
		if client.SendToClient(event.Name, event.Data) {
			return nil
		}
		return context.Canceled
	})
	if err != nil {
		client.SendToClient("error", err.Error())
		return nil, err
	}

	evidenceCount = result.TraceSummary.EvidenceCount
	failedToolCount = result.TraceSummary.FailedToolCount
	answerLength = len([]rune(result.Answer))
	return &v1.ChatStreamRes{}, nil
}
