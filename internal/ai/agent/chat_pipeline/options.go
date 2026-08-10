package chat_pipeline

import (
	"github.com/Thadopz/telltale/internal/ai/contextbudget"
	"github.com/Thadopz/telltale/internal/authz"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

type ChatAgentOptions struct {
	ChatModel              model.ToolCallingChatModel
	FailoverModel          model.ToolCallingChatModel
	Tools                  []tool.BaseTool
	MaxIterations          int
	ModelRetries           *int
	ContextProfile         *contextbudget.ModelContextProfile
	FailoverContextProfile *contextbudget.ModelContextProfile
	PromptOverheadTokens   int
	DisableToolSearch      bool
	AgentScope             authz.AgentScope
}
