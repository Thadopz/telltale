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

type MemoryItem struct {
	ID                   int64   `json:"id"`
	MemoryKey            string  `json:"memory_key"`
	Kind                 string  `json:"kind"`
	Content              string  `json:"content"`
	Confidence           float64 `json:"confidence"`
	Status               string  `json:"status"`
	VectorStatus         string  `json:"vector_status"`
	SourceConversationID string  `json:"source_conversation_id"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type MemoryCandidateItem struct {
	ID                   int64   `json:"id"`
	MemoryKey            string  `json:"memory_key"`
	Kind                 string  `json:"kind"`
	Content              string  `json:"content"`
	Confidence           float64 `json:"confidence"`
	SourceConversationID string  `json:"source_conversation_id"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type MemoryListReq struct {
	g.Meta `path:"/memories" method:"get" summary:"List durable memories"`
}

type MemoryListRes struct {
	Items []MemoryItem `json:"items"`
}

type MemoryDeleteReq struct {
	g.Meta   `path:"/memories/{MemoryId}" method:"delete" summary:"Delete durable memory"`
	MemoryId int64 `in:"path" json:"memoryId"`
}

type MemoryDeleteRes struct{}

type MemoryCandidateListReq struct {
	g.Meta `path:"/memory_candidates" method:"get" summary:"List pending durable memory candidates"`
}

type MemoryCandidateListRes struct {
	Items []MemoryCandidateItem `json:"items"`
}

type MemoryCandidateConfirmReq struct {
	g.Meta      `path:"/memory_candidates/{CandidateId}/confirm" method:"post" summary:"Confirm durable memory candidate"`
	CandidateId int64 `in:"path" json:"candidateId"`
}

type MemoryCandidateConfirmRes struct {
	Memory MemoryItem `json:"memory"`
}

type MemoryCandidateRejectReq struct {
	g.Meta      `path:"/memory_candidates/{CandidateId}/reject" method:"post" summary:"Reject durable memory candidate"`
	CandidateId int64 `in:"path" json:"candidateId"`
}

type MemoryCandidateRejectRes struct{}
