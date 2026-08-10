package eval

import (
	"github.com/Thadopz/telltale/internal/ai/agent/trace"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"time"
)

type Mode string

const (
	ModeContract            Mode = "contract"
	ModeRouting             Mode = "routing"
	ModeSmoke               Mode = "smoke"
	ModeContextStress       Mode = "context_stress"
	ModeQueryResultPressure Mode = "query_result_pressure"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CaseContext struct {
	Summary       string `json:"summary,omitempty"`
	SessionState  string `json:"session_state,omitempty"`
	DurableMemory string `json:"durable_memory,omitempty"`
	ToolResults   string `json:"tool_results,omitempty"`
}

type ModelStep struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Content   string         `json:"content,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type ToolParameterFixture struct {
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
}

type ToolFixture struct {
	Name        string                          `json:"name"`
	Description string                          `json:"description,omitempty"`
	Parameters  map[string]ToolParameterFixture `json:"parameters,omitempty"`
	Output      string                          `json:"output,omitempty"`
	Error       string                          `json:"error,omitempty"`
}

type ContextBudgetSpec struct {
	ModelName              string  `json:"model_name,omitempty"`
	ContextWindowTokens    int     `json:"context_window_tokens,omitempty"`
	MaxOutputTokens        int     `json:"max_output_tokens,omitempty"`
	WorkingPromptCapTokens int     `json:"working_prompt_cap_tokens,omitempty"`
	SafetyMarginRatio      float64 `json:"safety_margin_ratio,omitempty"`
	MinSafetyMarginTokens  int     `json:"min_safety_margin_tokens,omitempty"`
}

type ContextStressSpec struct {
	CriticalFacts        []string          `json:"critical_facts"`
	HistoryNoisePairs    int               `json:"history_noise_pairs,omitempty"`
	SummaryNoiseChars    int               `json:"summary_noise_chars,omitempty"`
	ToolResultNoiseChars int               `json:"tool_result_noise_chars,omitempty"`
	DistractorToolCount  int               `json:"distractor_tool_count,omitempty"`
	NormalProfile        ContextBudgetSpec `json:"normal_profile,omitempty"`
	StressProfile        ContextBudgetSpec `json:"stress_profile,omitempty"`
}

type QueryResultPressureSpec struct {
	TargetTool      string `json:"target_tool,omitempty"`
	DistractorCount int    `json:"distractor_count,omitempty"`
	SourceRows      int    `json:"source_rows,omitempty"`
	ReturnedRows    int    `json:"returned_rows,omitempty"`
	AgentMaxRows    int    `json:"agent_max_rows,omitempty"`
	AgentMaxBytes   int    `json:"agent_max_bytes,omitempty"`
	SQLTextChars    int    `json:"sql_text_chars,omitempty"`
}

type Expected struct {
	RequiredAgents      []string            `json:"required_agents,omitempty"`
	RequiredAgentsAny   []string            `json:"required_agents_any,omitempty"`
	ForbiddenAgents     []string            `json:"forbidden_agents,omitempty"`
	RequiredTools       []string            `json:"required_tools,omitempty"`
	RequiredToolsAny    []string            `json:"required_tools_any,omitempty"`
	ForbiddenTools      []string            `json:"forbidden_tools,omitempty"`
	ArgumentContains    map[string][]string `json:"argument_contains,omitempty"`
	AnswerContains      []string            `json:"answer_contains,omitempty"`
	AnswerContainsAny   []string            `json:"answer_contains_any,omitempty"`
	AnswerNotContains   []string            `json:"answer_not_contains,omitempty"`
	EvidenceContains    []string            `json:"evidence_contains,omitempty"`
	EvidenceContainsAny []string            `json:"evidence_contains_any,omitempty"`
	RequireEvidenceID   bool                `json:"require_evidence_id,omitempty"`
	ForbidToolCalls     bool                `json:"forbid_tool_calls,omitempty"`
	MaxToolCalls        int                 `json:"max_tool_calls,omitempty"`
	MaxModelCalls       int                 `json:"max_model_calls,omitempty"`
	MinModelErrors      int                 `json:"min_model_errors,omitempty"`
	JudgeCriteria       []string            `json:"judge_criteria,omitempty"`
	MinJudgeScore       float64             `json:"min_judge_score,omitempty"`
	RequiredClaimTypes  []string            `json:"required_claim_types,omitempty"`
	ClaimCoverage       map[string]string   `json:"claim_coverage,omitempty"`
}

type Case struct {
	ID                  string                   `json:"id"`
	Category            string                   `json:"category"`
	Query               string                   `json:"query"`
	History             []ChatMessage            `json:"history,omitempty"`
	Context             CaseContext              `json:"context,omitempty"`
	ModelSteps          []ModelStep              `json:"model_steps,omitempty"`
	FailoverModelSteps  []ModelStep              `json:"failover_model_steps,omitempty"`
	ToolFixtures        []ToolFixture            `json:"tool_fixtures,omitempty"`
	ContextStress       *ContextStressSpec       `json:"context_stress,omitempty"`
	QueryResultPressure *QueryResultPressureSpec `json:"query_result_pressure,omitempty"`
	Claims              *evidence.ClaimSet       `json:"claims,omitempty"`
	Expected            Expected                 `json:"expected,omitempty"`
}

type ContextEvidence struct {
	SummaryChars               int      `json:"summary_chars"`
	SessionStateChars          int      `json:"session_state_chars"`
	DurableMemoryChars         int      `json:"durable_memory_chars"`
	ToolResultsChars           int      `json:"tool_results_chars"`
	HistoryMessages            int      `json:"history_messages"`
	UseKnowledge               bool     `json:"use_knowledge"`
	PromptContextChars         int      `json:"prompt_context_chars"`
	Variant                    string   `json:"variant,omitempty"`
	RegisteredToolCount        int      `json:"registered_tool_count,omitempty"`
	EstimatedPromptTokens      int      `json:"estimated_prompt_tokens,omitempty"`
	FinalBudgetTokens          int      `json:"final_budget_tokens,omitempty"`
	InputLimitTokens           int      `json:"input_limit_tokens,omitempty"`
	PromptOverheadTokens       int      `json:"prompt_overhead_tokens,omitempty"`
	SafetyMarginTokens         int      `json:"safety_margin_tokens,omitempty"`
	ContextWindowTokens        int      `json:"context_window_tokens,omitempty"`
	CalibrationFactor          float64  `json:"calibration_factor,omitempty"`
	TrimmedLayers              []string `json:"trimmed_layers,omitempty"`
	CriticalFacts              []string `json:"critical_facts,omitempty"`
	CriticalFactsSeen          []string `json:"critical_facts_seen,omitempty"`
	MissingCriticalFacts       []string `json:"missing_critical_facts,omitempty"`
	DistractorToolCalls        []string `json:"distractor_tool_calls,omitempty"`
	ModelVisibleToolMarkerHits []string `json:"model_visible_tool_marker_hits,omitempty"`
	CurrentRequestPresent      bool     `json:"current_request_present,omitempty"`
	QuerySourceRows            int      `json:"query_source_rows,omitempty"`
	QueryReturnedRows          int      `json:"query_returned_rows,omitempty"`
	QueryMaxRows               int      `json:"query_max_rows,omitempty"`
	QueryReturnedBytes         int      `json:"query_returned_bytes,omitempty"`
	QueryMaxBytes              int      `json:"query_max_bytes,omitempty"`
	QueryTruncated             bool     `json:"query_truncated,omitempty"`
	QueryEvidenceID            string   `json:"query_evidence_id,omitempty"`
}

type ToolEvidence struct {
	Name       string `json:"name"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	EvidenceID string `json:"evidence_id,omitempty"`
	Elapsed    int64  `json:"elapsed_ms,omitempty"`
}

type ExecutionMetrics struct {
	ModelCalls       int `json:"model_calls"`
	ModelErrors      int `json:"model_errors"`
	AgentCalls       int `json:"agent_calls"`
	ToolCalls        int `json:"tool_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type JudgeResult struct {
	Enabled          bool    `json:"enabled"`
	Pass             bool    `json:"pass"`
	Score            float64 `json:"score"`
	Reason           string  `json:"reason,omitempty"`
	Error            string  `json:"error,omitempty"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
}

type EvalScores struct {
	RouteScore    float64  `json:"route_score"`
	ToolScore     float64  `json:"tool_score"`
	EvidenceScore float64  `json:"evidence_score"`
	ClaimScore    float64  `json:"claim_score"`
	AnswerScore   float64  `json:"answer_score"`
	SafetyScore   float64  `json:"safety_score"`
	Pass          bool     `json:"pass"`
	Reasons       []string `json:"reasons,omitempty"`
}

type EvalCaseResult struct {
	ID                   string                    `json:"id"`
	Attempt              int                       `json:"attempt"`
	Variant              string                    `json:"variant,omitempty"`
	Category             string                    `json:"category"`
	Mode                 Mode                      `json:"mode"`
	Query                string                    `json:"query"`
	ConversationID       string                    `json:"conversation_id"`
	Context              ContextEvidence           `json:"context"`
	Metrics              ExecutionMetrics          `json:"metrics"`
	Judge                JudgeResult               `json:"judge"`
	ToolCalls            []ToolEvidence            `json:"tool_calls,omitempty"`
	TraceSteps           []trace.AgentStep         `json:"trace_steps,omitempty"`
	Answer               string                    `json:"answer"`
	DraftAnswer          string                    `json:"draft_answer,omitempty"`
	ClaimSet             *evidence.ClaimSet        `json:"claim_set,omitempty"`
	CoverageReports      []evidence.CoverageReport `json:"coverage_reports,omitempty"`
	ClaimExtractionError string                    `json:"claim_extraction_error,omitempty"`
	LocalizationMode     string                    `json:"localization_mode,omitempty"`
	Error                string                    `json:"error,omitempty"`
	ElapsedMS            int64                     `json:"elapsed_ms"`
	Scores               EvalScores                `json:"scores"`
	StartedAt            time.Time                 `json:"started_at"`
}

type RunSummary struct {
	Mode                 Mode               `json:"mode"`
	Cases                int                `json:"cases"`
	StableCases          int                `json:"stable_cases"`
	Total                int                `json:"total"`
	Passed               int                `json:"passed"`
	Failed               int                `json:"failed"`
	PassRate             float64            `json:"pass_rate"`
	RequiredPassRate     float64            `json:"required_pass_rate"`
	GatePass             bool               `json:"gate_pass"`
	SafetyAttempts       int                `json:"safety_attempts"`
	SafetyAttemptsPassed int                `json:"safety_attempts_passed"`
	SafetyGatePass       bool               `json:"safety_gate_pass"`
	VariantGatePass      bool               `json:"variant_gate_pass"`
	AverageModelCalls    float64            `json:"average_model_calls"`
	AverageToolCalls     float64            `json:"average_tool_calls"`
	AverageTotalTokens   float64            `json:"average_total_tokens"`
	AverageRouteScore    float64            `json:"average_route_score"`
	AverageToolScore     float64            `json:"average_tool_score"`
	AverageEvidenceScore float64            `json:"average_evidence_score"`
	AverageClaimScore    float64            `json:"average_claim_score"`
	AverageAnswerScore   float64            `json:"average_answer_score"`
	AverageSafetyScore   float64            `json:"average_safety_score"`
	ToolCallCounts       map[string]int     `json:"tool_call_counts"`
	KnowledgeUsed        int                `json:"knowledge_used"`
	FailureReasons       []string           `json:"failure_reasons,omitempty"`
	CasePassRates        map[string]float64 `json:"case_pass_rates"`
}

type RunOptions struct {
	Mode                 Mode
	CasesPath            string
	ReportsDir           string
	RunID                string
	CaseTimeoutSeconds   int
	Repetitions          int
	MinimumPassRate      float64
	EnableJudge          bool
	RealModel            bool
	ContextStressVariant string
}

type RunResult struct {
	Summary      RunSummary
	Results      []EvalCaseResult
	JSONLPath    string
	MarkdownPath string
}
