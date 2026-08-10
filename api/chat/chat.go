// =================================================================================
// Code generated and maintained by GoFrame CLI tool. DO NOT EDIT.
// =================================================================================

package chat

import (
	"context"

	"github.com/Thadopz/telltale/api/chat/v1"
)

type IChatV1 interface {
	Chat(ctx context.Context, req *v1.ChatReq) (res *v1.ChatRes, err error)
	ChatStream(ctx context.Context, req *v1.ChatStreamReq) (res *v1.ChatStreamRes, err error)
	FileUpload(ctx context.Context, req *v1.FileUploadReq) (res *v1.FileUploadRes, err error)
	ActionPrepare(ctx context.Context, req *v1.ActionPrepareReq) (res *v1.ActionPrepareRes, err error)
	ActionConfirm(ctx context.Context, req *v1.ActionConfirmReq) (res *v1.ActionConfirmRes, err error)
	ActionExecute(ctx context.Context, req *v1.ActionExecuteReq) (res *v1.ActionExecuteRes, err error)
	MemoryList(ctx context.Context, req *v1.MemoryListReq) (res *v1.MemoryListRes, err error)
	MemoryDelete(ctx context.Context, req *v1.MemoryDeleteReq) (res *v1.MemoryDeleteRes, err error)
	MemoryCandidateList(ctx context.Context, req *v1.MemoryCandidateListReq) (res *v1.MemoryCandidateListRes, err error)
	MemoryCandidateConfirm(ctx context.Context, req *v1.MemoryCandidateConfirmReq) (res *v1.MemoryCandidateConfirmRes, err error)
	MemoryCandidateReject(ctx context.Context, req *v1.MemoryCandidateRejectReq) (res *v1.MemoryCandidateRejectRes, err error)
}
