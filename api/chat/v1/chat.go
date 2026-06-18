package v1

import (
	"github.com/gogf/gf/v2/frame/g"
)

type ChatReq struct {
	g.Meta         `path:"/chat" method:"post" summary:"对话"`
	Id             string
	UserId         string
	ConversationId string
	Question       string
	KnowledgeMode  string `json:"knowledgeMode"`
}

type ChatRes struct {
	Answer string `json:"answer"`
}

type ChatStreamReq struct {
	g.Meta         `path:"/chat_stream" method:"post" summary:"流式对话"`
	Id             string
	UserId         string
	ConversationId string
	Question       string
	KnowledgeMode  string `json:"knowledgeMode"`
}

type ChatStreamRes struct {
}

type FileUploadReq struct {
	g.Meta `path:"/upload" method:"post" mime:"multipart/form-data" summary:"文件上传"`
}

type FileUploadRes struct {
	FileName string `json:"fileName" dc:"保存的文件名"`
	FilePath string `json:"filePath" dc:"文件保存路径"`
	FileSize int64  `json:"fileSize" dc:"文件大小(字节)"`
}

type AIOpsReq struct {
	g.Meta `path:"/ai_ops" method:"post" summary:"AI运维"`
}

type AIOpsRes struct {
	Result string   `json:"result"`
	Detail []string `json:"detail"`
}

type ActionPrepareReq struct {
	g.Meta        `path:"/actions/prepare" method:"post" summary:"Prepare high-risk action"`
	UserId        string `json:"userId"`
	ToolName      string `json:"tool_name"`
	ArgumentsJSON string `json:"arguments_json"`
}

type ActionPrepareRes struct {
	Token           string `json:"token"`
	ToolName        string `json:"tool_name"`
	ArgumentsHash   string `json:"arguments_hash"`
	Risk            string `json:"risk"`
	RequiresConfirm bool   `json:"requires_confirm"`
	ExpiresAt       string `json:"expires_at"`
}

type ActionConfirmReq struct {
	g.Meta        `path:"/actions/confirm" method:"post" summary:"Confirm high-risk action"`
	UserId        string `json:"userId"`
	Token         string `json:"token"`
	ArgumentsHash string `json:"arguments_hash"`
}

type ActionConfirmRes struct {
	Token         string `json:"token"`
	ToolName      string `json:"tool_name"`
	ArgumentsHash string `json:"arguments_hash"`
	Status        string `json:"status"`
}

type ActionExecuteReq struct {
	g.Meta        `path:"/actions/execute" method:"post" summary:"Execute confirmed high-risk action"`
	UserId        string `json:"userId"`
	Token         string `json:"token"`
	ToolName      string `json:"tool_name"`
	ArgumentsJSON string `json:"arguments_json"`
}

type ActionExecuteRes struct {
	ToolName      string `json:"tool_name"`
	ArgumentsHash string `json:"arguments_hash"`
	Output        string `json:"output"`
}
