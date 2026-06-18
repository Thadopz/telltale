package mem

import (
	"SREagent/internal/ai/evidence"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	toolResultContextLimit    = 5
	toolResultSummaryMaxChars = 1600
)

type ToolResult struct {
	ToolName      string
	InputJSON     string
	OutputSummary string
	OutputJSON    string
	Status        string
}

type toolResultRow struct {
	ToolName      string `json:"tool_name"`
	OutputSummary string `json:"output_summary"`
	Status        string `json:"status"`
}

type evidenceResultRow struct {
	ToolName string `json:"tool_name"`
	Output   string `json:"output"`
	Status   string `json:"status"`
}

type toolResultRecorder struct {
	storageID string
	mu        sync.Mutex
	inputs    map[string][]string
}

func ToolResultCallback(userID string, conversationID string) callbacks.Handler {
	scope := normalizeMemoryScope(userID, conversationID)
	recorder := &toolResultRecorder{
		storageID: strings.TrimSpace(scope.StorageID),
		inputs:    make(map[string][]string),
	}
	builder := callbacks.NewHandlerBuilder()
	builder.OnStartFn(recorder.onStart)
	builder.OnEndFn(recorder.onEnd)
	builder.OnErrorFn(recorder.onError)
	return builder.Build()
}

func SaveToolResult(ctx context.Context, userID string, conversationID string, result ToolResult) error {
	scope := normalizeMemoryScope(userID, conversationID)
	return saveToolResultByStorageID(ctx, scope.StorageID, result)
}

func loadRecentToolResults(ctx context.Context, conversationID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", nil
	}

	var results []toolResultRow
	if err := g.DB().Model("tool_results").
		Ctx(ctx).
		Fields("tool_name", "output_summary", "status").
		Where("conversation_id", conversationID).
		OrderDesc("id").
		Limit(toolResultContextLimit).
		Scan(&results); err != nil {
		return "", err
	}
	return formatToolResults(results), nil
}

func formatToolResults(results []toolResultRow) string {
	lines := make([]string, 0, len(results))
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		name := strings.TrimSpace(result.ToolName)
		summary := strings.TrimSpace(result.OutputSummary)
		if name == "" || summary == "" {
			continue
		}
		status := strings.TrimSpace(result.Status)
		if status == "" {
			status = "success"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s]: %s", name, status, summary))
	}
	return strings.Join(lines, "\n")
}

func (r *toolResultRecorder) onStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if r == nil || info == nil || info.Component != components.ComponentOfTool {
		return ctx
	}
	toolInput := tool.ConvCallbackInput(input)
	if toolInput == nil {
		return ctx
	}
	r.pushInput(info.Name, toolInput.ArgumentsInJSON)
	return ctx
}

func (r *toolResultRecorder) onEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if r == nil || info == nil || info.Component != components.ComponentOfTool {
		return ctx
	}
	toolOutput := tool.ConvCallbackOutput(output)
	response := ""
	if toolOutput != nil {
		response = toolOutput.Response
	}
	parsed := evidence.Parse(response).Envelope
	_ = saveToolResultByStorageID(ctx, r.storageID, ToolResult{
		ToolName:      info.Name,
		InputJSON:     r.popInput(info.Name),
		OutputSummary: formatEnvelopeSummary(parsed, response),
		OutputJSON:    response,
		Status:        parsed.Status,
	})
	return ctx
}

func (r *toolResultRecorder) onError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if r == nil || info == nil || info.Component != components.ComponentOfTool {
		return ctx
	}
	_ = saveToolResultByStorageID(ctx, r.storageID, ToolResult{
		ToolName:      info.Name,
		InputJSON:     r.popInput(info.Name),
		OutputSummary: formatEnvelopeSummary(evidence.NewError(err, "tool callback error"), ""),
		Status:        evidence.StatusError,
	})
	return ctx
}

func formatEnvelopeSummary(env evidence.Envelope, raw string) string {
	summary := strings.TrimSpace(env.Summary)
	if summary == "" {
		summary = strings.TrimSpace(env.Message)
	}
	if summary == "" {
		summary = strings.TrimSpace(env.Error)
	}
	if summary == "" {
		summary = strings.TrimSpace(raw)
	}
	if env.CanUseAsEvidence && strings.TrimSpace(env.EvidenceID) != "" {
		return fmt.Sprintf("%s evidence_id=%s data_status=%s", summary, env.EvidenceID, env.DataStatus)
	}
	if strings.TrimSpace(env.FailedQueryID) != "" {
		return fmt.Sprintf("%s failed_query_id=%s data_status=%s", summary, env.FailedQueryID, env.DataStatus)
	}
	return summary
}

func saveToolResultByStorageID(ctx context.Context, storageID string, result ToolResult) error {
	storageID = strings.TrimSpace(storageID)
	result.ToolName = strings.TrimSpace(result.ToolName)
	if storageID == "" || result.ToolName == "" {
		return nil
	}
	if strings.TrimSpace(result.Status) == "" {
		result.Status = "success"
	}

	data := g.Map{
		"conversation_id": storageID,
		"tool_name":       result.ToolName,
		"output_summary":  trimSessionStateText(result.OutputSummary, toolResultSummaryMaxChars),
		"status":          result.Status,
	}
	structured := g.Map{}
	if json.Valid([]byte(result.InputJSON)) {
		data["input"] = result.InputJSON
	}
	if json.Valid([]byte(result.OutputJSON)) {
		data["output"] = result.OutputJSON
		env := evidence.Parse(result.OutputJSON).Envelope
		structured = structuredEvidenceColumns(env)
		for key, value := range structured {
			data[key] = value
		}
	}
	_, err := g.DB().Model("tool_results").Ctx(ctx).Data(data).Insert()
	if err != nil && len(structured) > 0 && isUnknownStructuredEvidenceColumn(err) {
		for key := range structured {
			delete(data, key)
		}
		_, err = g.DB().Model("tool_results").Ctx(ctx).Data(data).Insert()
	}
	return err
}

func ListUsableEvidence(ctx context.Context, conversationID string, limit int) ([]evidence.EvidenceItem, error) {
	rows, err := loadEvidenceRows(ctx, conversationID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]evidence.EvidenceItem, 0, len(rows))
	for _, row := range rows {
		env := evidence.Parse(row.Output).Envelope
		if env.Status != evidence.StatusSuccess || !env.CanUseAsEvidence || strings.TrimSpace(env.EvidenceID) == "" || len(env.Observations) == 0 {
			continue
		}
		items = append(items, evidence.EvidenceItem{
			EvidenceID:   env.EvidenceID,
			ToolName:     row.ToolName,
			Scope:        env.Scope,
			Subject:      env.Subject,
			Observations: env.Observations,
			RawOutput:    json.RawMessage(row.Output),
			Summary:      env.Summary,
		})
	}
	return items, nil
}

func ListFailedQueries(ctx context.Context, conversationID string, limit int) ([]evidence.FailedQuery, error) {
	rows, err := loadEvidenceRows(ctx, conversationID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]evidence.FailedQuery, 0, len(rows))
	for _, row := range rows {
		env := evidence.Parse(row.Output).Envelope
		if strings.TrimSpace(env.FailedQueryID) == "" {
			continue
		}
		items = append(items, evidence.FailedQuery{
			FailedQueryID: env.FailedQueryID,
			ToolName:      row.ToolName,
			Status:        env.Status,
			Summary:       env.Summary,
		})
	}
	return items, nil
}

func FindEvidenceByID(ctx context.Context, conversationID string, evidenceID string) (*evidence.EvidenceItem, error) {
	items, err := ListUsableEvidence(ctx, conversationID, toolResultContextLimit)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.EvidenceID == evidenceID {
			found := item
			return &found, nil
		}
	}
	return nil, nil
}

func loadEvidenceRows(ctx context.Context, conversationID string, limit int) ([]evidenceResultRow, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = toolResultContextLimit
	}
	var rows []evidenceResultRow
	err := g.DB().Model("tool_results").
		Ctx(ctx).
		Fields("tool_name", "output", "status").
		Where("conversation_id", conversationID).
		OrderDesc("id").
		Limit(limit).
		Scan(&rows)
	return rows, err
}

func structuredEvidenceColumns(env evidence.Envelope) g.Map {
	columns := g.Map{
		"evidence_id":         env.EvidenceID,
		"failed_query_id":     env.FailedQueryID,
		"can_use_as_evidence": env.CanUseAsEvidence,
		"data_status":         env.DataStatus,
	}
	if subject, err := json.Marshal(env.Subject); err == nil && string(subject) != "{}" {
		columns["subject"] = string(subject)
	}
	if scope, err := json.Marshal(env.Scope); err == nil && string(scope) != "{}" {
		columns["scope"] = string(scope)
	}
	if observations, err := json.Marshal(env.Observations); err == nil && string(observations) != "null" {
		columns["observations"] = string(observations)
	}
	return columns
}

func isUnknownStructuredEvidenceColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, column := range []string{"evidence_id", "failed_query_id", "can_use_as_evidence", "data_status", "subject", "scope", "observations"} {
		if strings.Contains(msg, column) {
			return true
		}
	}
	return false
}

func (r *toolResultRecorder) pushInput(name string, input string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputs[name] = append(r.inputs[name], input)
}

func (r *toolResultRecorder) popInput(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.inputs[name]
	if len(values) == 0 {
		return ""
	}
	value := values[0]
	r.inputs[name] = values[1:]
	return value
}
