package chat_pipeline

import (
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

type ChatAgentOptions struct {
	ChatModel     model.ToolCallingChatModel
	FailoverModel model.ToolCallingChatModel
	Tools         []tool.BaseTool
	MaxIterations int
	ModelRetries  *int
}
