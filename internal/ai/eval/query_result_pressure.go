package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	queryResultPressureTargetTool = "db_slow_queries"
	queryResultPressureMaxTools   = 100
)

func normalizeQueryResultPressureSpec(input *QueryResultPressureSpec) QueryResultPressureSpec {
	spec := QueryResultPressureSpec{
		TargetTool:      queryResultPressureTargetTool,
		DistractorCount: queryResultPressureMaxTools,
		SourceRows:      10000,
		ReturnedRows:    20,
		AgentMaxRows:    50,
		AgentMaxBytes:   64 * 1024,
		SQLTextChars:    1000,
	}
	if input != nil {
		if strings.TrimSpace(input.TargetTool) != "" {
			spec.TargetTool = strings.TrimSpace(input.TargetTool)
		}
		if input.DistractorCount > 0 {
			spec.DistractorCount = input.DistractorCount
		}
		if input.SourceRows > 0 {
			spec.SourceRows = input.SourceRows
		}
		if input.ReturnedRows > 0 {
			spec.ReturnedRows = input.ReturnedRows
		}
		if input.AgentMaxRows > 0 {
			spec.AgentMaxRows = input.AgentMaxRows
		}
		if input.AgentMaxBytes > 0 {
			spec.AgentMaxBytes = input.AgentMaxBytes
		}
		if input.SQLTextChars > 0 {
			spec.SQLTextChars = input.SQLTextChars
		}
	}
	if spec.DistractorCount > queryResultPressureMaxTools {
		spec.DistractorCount = queryResultPressureMaxTools
	}
	if spec.SourceRows < spec.ReturnedRows {
		spec.SourceRows = spec.ReturnedRows
	}
	if spec.ReturnedRows > spec.AgentMaxRows {
		spec.ReturnedRows = spec.AgentMaxRows
	}
	return spec
}

func prepareQueryResultPressureCase(input Case) (Case, error) {
	spec := normalizeQueryResultPressureSpec(input.QueryResultPressure)
	if spec.TargetTool != queryResultPressureTargetTool {
		return Case{}, fmt.Errorf("query result pressure target must be %q", queryResultPressureTargetTool)
	}
	prepared := input
	prepared.QueryResultPressure = &spec
	prepared.ToolFixtures = queryResultPressureToolFixtures(spec)
	if err := validateQueryResultPressureToolFixtures(prepared.ToolFixtures, spec); err != nil {
		return Case{}, err
	}
	prepared.Context.Summary = strings.TrimSpace(input.Context.Summary) + "\nThe database observation contains a bounded top-N result."
	prepared.Context.ToolResults = strings.TrimSpace(input.Context.ToolResults) + "\nA large slow-query source is available through the fixed semantic observer."
	return prepared, nil
}

func queryResultPressureToolFixtures(spec QueryResultPressureSpec) []ToolFixture {
	fixtures := contextStressToolFixtures(spec.DistractorCount)
	if len(fixtures) == 0 {
		return nil
	}
	fixtures[0] = ToolFixture{
		Name:        queryResultPressureTargetTool,
		Description: "Inspect bounded top slow-query records from the configured business database observation source.",
		Parameters: map[string]ToolParameterFixture{
			"target": {
				Type:        "string",
				Description: "Logical database target name.",
			},
			"sort_by": {
				Type:        "string",
				Description: "Generic sort mode for the configured adapter.",
				Enum:        []string{"latency", "frequency", "resource", "recent"},
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum number of returned rows; the server applies the hard limit.",
			},
		},
		Output: queryResultPressureOutput(spec),
	}
	return fixtures
}

func validateQueryResultPressureToolFixtures(fixtures []ToolFixture, spec QueryResultPressureSpec) error {
	if len(fixtures) != spec.DistractorCount+1 {
		return fmt.Errorf("query result pressure expected %d visible tools, got %d", spec.DistractorCount+1, len(fixtures))
	}
	seenTarget := false
	for _, fixture := range fixtures {
		if fixture.Name == queryResultPressureTargetTool {
			seenTarget = true
		}
		if fixture.Name == "mysql_crud" {
			return errors.New("query result pressure exposed mysql_crud in the agent tool list")
		}
	}
	if !seenTarget {
		return errors.New("query result pressure target tool is missing from the agent tool list")
	}
	if markers := contextStressToolMarkerHits(fixtures); len(markers) > 0 {
		return fmt.Errorf("query result pressure tool metadata contains forbidden markers: %s", strings.Join(markers, ", "))
	}
	return nil
}

func queryResultPressureOutput(spec QueryResultPressureSpec) string {
	rows := make([]map[string]any, 0, spec.ReturnedRows)
	observations := make([]evidence.Observation, 0, spec.ReturnedRows)
	for index := 0; index < spec.ReturnedRows; index++ {
		digest := fmt.Sprintf("SELECT callback_state FROM payment_callback_events WHERE id = ? digest_%04d", index+1)
		if len([]rune(digest)) > spec.SQLTextChars {
			digest = string([]rune(digest)[:spec.SQLTextChars])
		}
		row := map[string]any{
			"db":            "card_service",
			"sql_digest":    digest,
			"exec_count":    10000 - index,
			"avg_seconds":   float64(12-index%5) + 0.5,
			"rows_examined": 5000 + index,
		}
		rows = append(rows, row)
		observations = append(observations, evidence.Observation{
			Kind:    evidence.ObservationSlowSQL,
			Scope:   evidence.EvidenceScope{Fields: map[string]string{"source": "performance_schema", "sort_by": "avg_latency", "limit": fmt.Sprintf("%d", spec.AgentMaxRows)}},
			Fields:  row,
			Summary: fmt.Sprintf("Top slow SQL record %d is within the returned bounded scope.", index+1),
		})
	}
	encodedRows, _ := json.Marshal(rows)
	metadata := evidence.ResultMetadata{
		ReturnedRows:     len(rows),
		MaxRows:          spec.AgentMaxRows,
		ReturnedBytes:    len(encodedRows),
		MaxBytes:         spec.AgentMaxBytes,
		Truncated:        spec.SourceRows > len(rows),
		TruncationReason: "source_limit",
		Projection:       []string{"db", "sql_digest", "exec_count", "avg_seconds", "rows_examined"},
		SortBy:           "avg_latency",
	}
	envelope := evidence.NewDataWithResultMetadata(
		map[string]any{
			"kind":            "slow_sql",
			"source":          "performance_schema",
			"database_engine": "mysql",
			"target":          "default",
			"capability":      "slow_queries",
			"source_rows":     spec.SourceRows,
			"rows":            rows,
		},
		fmt.Sprintf("database slow-query observation returned the top %d rows from a larger source", len(rows)),
		observations,
		metadata,
	)
	envelope.EvidenceID = "ev_sql_slow_pressure"
	return evidence.Marshal(envelope)
}

type queryResultPressureModel struct {
	mu          sync.Mutex
	toolInfos   []*schema.ToolInfo
	calls       int
	targetCalls int
	evidenceID  string
	metadata    evidence.ResultMetadata
	answer      string
}

func newQueryResultPressureModel() *queryResultPressureModel {
	return &queryResultPressureModel{}
}

func (m *queryResultPressureModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.calls == 0 {
		m.calls++
		m.targetCalls++
		arguments, err := json.Marshal(map[string]any{
			"target":  "default",
			"sort_by": "latency",
			"limit":   20,
		})
		if err != nil {
			return nil, err
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "query_pressure_slow_sql",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      queryResultPressureTargetTool,
				Arguments: string(arguments),
			},
		}}), nil
	}

	envelope, found := latestQueryResultPressureEnvelope(messages)
	if !found {
		return nil, errors.New("query_result_pressure_result_missing")
	}
	if envelope.Status != evidence.StatusSuccess || envelope.EvidenceID == "" {
		return nil, errors.New("query_result_pressure_evidence_missing")
	}
	if envelope.DataStatus != evidence.DataPartial || !envelope.ResultMetadata.Truncated {
		return nil, errors.New("query_result_pressure_partial_metadata_missing")
	}
	if envelope.ResultMetadata.ReturnedRows <= 0 || envelope.ResultMetadata.MaxRows <= 0 {
		return nil, errors.New("query_result_pressure_row_budget_missing")
	}
	if envelope.ResultMetadata.ReturnedRows > 50 || envelope.ResultMetadata.ReturnedBytes > 64*1024 {
		return nil, errors.New("query_result_pressure_budget_exceeded")
	}
	m.evidenceID = envelope.EvidenceID
	m.metadata = envelope.ResultMetadata
	m.calls++
	m.answer = fmt.Sprintf("The slow-SQL result is partial top-N evidence: %d rows returned with a %d-row limit and %d-byte result budget. Evidence ID: %s. It must not be interpreted as the complete set of database queries. Next check: inspect active queries and wait events, then correlate with application connection-pool metrics.", envelope.ResultMetadata.ReturnedRows, envelope.ResultMetadata.MaxRows, envelope.ResultMetadata.MaxBytes, envelope.EvidenceID)
	return schema.AssistantMessage(m.answer, nil), nil
}

func (m *queryResultPressureModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (m *queryResultPressureModel) WithTools(toolInfos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolInfos = append([]*schema.ToolInfo(nil), toolInfos...)
	return m, nil
}

func (m *queryResultPressureModel) EvidenceID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evidenceID
}

func (m *queryResultPressureModel) Metadata() evidence.ResultMetadata {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.metadata
}

func latestQueryResultPressureEnvelope(messages []*schema.Message) (evidence.Envelope, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message == nil || message.Role != schema.Tool || message.ToolName != queryResultPressureTargetTool {
			continue
		}
		parsed := evidence.Parse(message.Content)
		if !parsed.IsJSON {
			return evidence.Envelope{}, false
		}
		return parsed.Envelope, true
	}
	return evidence.Envelope{}, false
}

func recordQueryResultPressureDiagnostics(result *EvalCaseResult, evalCase Case, pressureModel *queryResultPressureModel) {
	if result == nil || evalCase.QueryResultPressure == nil {
		return
	}
	spec := normalizeQueryResultPressureSpec(evalCase.QueryResultPressure)
	result.Context.QuerySourceRows = spec.SourceRows
	if pressureModel != nil {
		result.Context.QueryReturnedRows = pressureModel.Metadata().ReturnedRows
		result.Context.QueryMaxRows = pressureModel.Metadata().MaxRows
		result.Context.QueryReturnedBytes = pressureModel.Metadata().ReturnedBytes
		result.Context.QueryMaxBytes = pressureModel.Metadata().MaxBytes
		result.Context.QueryTruncated = pressureModel.Metadata().Truncated
		result.Context.QueryEvidenceID = pressureModel.EvidenceID()
	}
}

var _ model.ToolCallingChatModel = (*queryResultPressureModel)(nil)
