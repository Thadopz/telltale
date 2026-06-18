// =================================================================================
// Code generated and maintained by GoFrame CLI tool. DO NOT EDIT.
// =================================================================================

package chat

import (
	"context"

	"SREagent/api/chat/v1"
)

type IChatV1 interface {
	Chat(ctx context.Context, req *v1.ChatReq) (res *v1.ChatRes, err error)
	ChatStream(ctx context.Context, req *v1.ChatStreamReq) (res *v1.ChatStreamRes, err error)
	FileUpload(ctx context.Context, req *v1.FileUploadReq) (res *v1.FileUploadRes, err error)
	AIOps(ctx context.Context, req *v1.AIOpsReq) (res *v1.AIOpsRes, err error)
	ActionPrepare(ctx context.Context, req *v1.ActionPrepareReq) (res *v1.ActionPrepareRes, err error)
	ActionConfirm(ctx context.Context, req *v1.ActionConfirmReq) (res *v1.ActionConfirmRes, err error)
	ActionExecute(ctx context.Context, req *v1.ActionExecuteReq) (res *v1.ActionExecuteRes, err error)
}
