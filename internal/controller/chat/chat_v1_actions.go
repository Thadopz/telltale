package chat

import (
	"context"
	v1 "github.com/Thadopz/telltale/api/chat/v1"
	"github.com/Thadopz/telltale/internal/actions"
	"github.com/Thadopz/telltale/internal/authz"
)

func (c *ControllerV1) ActionPrepare(ctx context.Context, req *v1.ActionPrepareReq) (res *v1.ActionPrepareRes, err error) {
	ctx = authz.WithPrincipal(ctx, authz.ConfiguredPrincipal(ctx))
	prepared, err := actions.Prepare(ctx, req.ToolName, req.ArgumentsJSON)
	if err != nil {
		return nil, err
	}
	return &v1.ActionPrepareRes{
		Token:           prepared.Token,
		ToolName:        prepared.ToolName,
		ArgumentsHash:   prepared.ArgumentsHash,
		Risk:            prepared.Risk,
		RequiresConfirm: prepared.RequiresConfirm,
		ExpiresAt:       prepared.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (c *ControllerV1) ActionConfirm(ctx context.Context, req *v1.ActionConfirmReq) (res *v1.ActionConfirmRes, err error) {
	ctx = authz.WithPrincipal(ctx, authz.ConfiguredPrincipal(ctx))
	confirmed, err := actions.Confirm(ctx, req.Token, req.ArgumentsHash)
	if err != nil {
		return nil, err
	}
	return &v1.ActionConfirmRes{
		Token:         confirmed.Token,
		ToolName:      confirmed.ToolName,
		ArgumentsHash: confirmed.ArgumentsHash,
		Status:        confirmed.Status,
	}, nil
}

func (c *ControllerV1) ActionExecute(ctx context.Context, req *v1.ActionExecuteReq) (res *v1.ActionExecuteRes, err error) {
	ctx = authz.WithPrincipal(ctx, authz.ConfiguredPrincipal(ctx))
	executed, err := actions.Execute(ctx, req.Token, req.ToolName, req.ArgumentsJSON)
	if err != nil {
		return nil, err
	}
	return &v1.ActionExecuteRes{
		ToolName:      executed.ToolName,
		ArgumentsHash: executed.ArgumentsHash,
		Output:        executed.Output,
	}, nil
}
