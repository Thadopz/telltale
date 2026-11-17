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

type ProfileItem struct {
	ID                   int64   `json:"id"`
	SlotKey              string  `json:"slot_key"`
	ValueType            string  `json:"value_type"`
	ValueJSON            string  `json:"value_json"`
	ValueText            string  `json:"value_text"`
	Revision             int     `json:"revision"`
	Status               string  `json:"status"`
	Confirmation         string  `json:"confirmation"`
	Confidence           float64 `json:"confidence"`
	SourceConversationID string  `json:"source_conversation_id"`
	ValidFrom            string  `json:"valid_from"`
	ValidUntil           string  `json:"valid_until,omitempty"`
	UpdatedAt            string  `json:"updated_at"`
}

type ProfileCandidateItem struct {
	ID                   int64   `json:"id"`
	SlotKey              string  `json:"slot_key"`
	ValueType            string  `json:"value_type"`
	ValueJSON            string  `json:"value_json"`
	ValueText            string  `json:"value_text"`
	Revision             int     `json:"revision"`
	Confidence           float64 `json:"confidence"`
	SourceConversationID string  `json:"source_conversation_id"`
	ExpiresAt            string  `json:"expires_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type EventItem struct {
	ID             string   `json:"id"`
	ConversationID string   `json:"conversation_id"`
	Summary        string   `json:"summary"`
	Tags           []string `json:"tags"`
	Entities       []string `json:"entities"`
	RecordedAt     string   `json:"recorded_at"`
	OccurredAt     string   `json:"occurred_at,omitempty"`
	TimeConfidence string   `json:"time_confidence"`
	EvidenceIDs    []string `json:"evidence_ids"`
	EvidenceStatus string   `json:"evidence_status"`
	Status         string   `json:"status"`
}

type ProfileListReq struct {
	g.Meta `path:"/profiles" method:"get" summary:"List current profile slots"`
	Limit  int `in:"query" json:"limit"`
}

type ProfileListRes struct {
	Items []ProfileItem `json:"items"`
}

type ProfileDeleteReq struct {
	g.Meta    `path:"/profiles/{ProfileId}" method:"delete" summary:"Delete a profile revision"`
	ProfileId int64 `in:"path" json:"profileId"`
	Revision  int   `in:"query" json:"revision"`
}

type ProfileDeleteRes struct{}

type ProfileCandidateListReq struct {
	g.Meta `path:"/profile_candidates" method:"get" summary:"List pending profile candidates"`
	Limit  int `in:"query" json:"limit"`
}

type ProfileCandidateListRes struct {
	Items []ProfileCandidateItem `json:"items"`
}

type ProfileCandidateConfirmReq struct {
	g.Meta      `path:"/profile_candidates/{CandidateId}/confirm" method:"post" summary:"Confirm a profile candidate"`
	CandidateId int64 `in:"path" json:"candidateId"`
	Revision    int   `json:"revision"`
}

type ProfileCandidateConfirmRes struct {
	Profile ProfileItem `json:"profile"`
}

type ProfileCandidateRejectReq struct {
	g.Meta      `path:"/profile_candidates/{CandidateId}/reject" method:"post" summary:"Reject a profile candidate"`
	CandidateId int64 `in:"path" json:"candidateId"`
	Revision    int   `json:"revision"`
}

type ProfileCandidateRejectRes struct{}

type EventListReq struct {
	g.Meta `path:"/events" method:"get" summary:"List memory events"`
	Limit  int `in:"query" json:"limit"`
}

type EventListRes struct {
	Items []EventItem `json:"items"`
}

type EventDeleteReq struct {
	g.Meta  `path:"/events/{EventId}" method:"delete" summary:"Delete a memory event"`
	EventId string `in:"path" json:"eventId"`
}

type EventDeleteRes struct{}

type MemoryBufferFlushReq struct {
	g.Meta         `path:"/memory/buffers/{ConversationId}/flush" method:"post" summary:"Flush a conversation memory buffer"`
	ConversationId string `in:"path" json:"conversationId"`
}

type MemoryBufferFlushRes struct {
	Status string `json:"status"`
}
