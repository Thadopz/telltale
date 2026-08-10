package sqlobserve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/gogf/gf/v2/frame/g"
)

func LoadResultBudget(ctx context.Context) ResultBudget {
	budget := DefaultResultBudget()
	budget.AgentMaxBytes = configPositiveInt(ctx, "tools.result_budget.agent_max_bytes", budget.AgentMaxBytes)
	budget.AgentMaxRows = configPositiveInt(ctx, "tools.result_budget.agent_max_rows", budget.AgentMaxRows)
	budget.HardMaxRows = configPositiveInt(ctx, "tools.result_budget.hard_max_rows", budget.HardMaxRows)
	budget.CLIMaxBytes = configPositiveInt(ctx, "tools.result_budget.cli_max_bytes", budget.CLIMaxBytes)
	budget.MaxSQLTextChars = configPositiveInt(ctx, "tools.result_budget.max_sql_text_chars", budget.MaxSQLTextChars)
	budget.MaxObservations = configPositiveInt(ctx, "tools.result_budget.max_observations", budget.MaxObservations)

	// Keep the old setting as a fallback for deployments that have not added
	// the new result_budget section yet.
	legacyRows := configPositiveInt(ctx, "tools.mysql.max_rows", 0)
	if legacyRows > 0 && configMissing(ctx, "tools.result_budget.hard_max_rows") {
		budget.HardMaxRows = legacyRows
	}
	if budget.AgentMaxRows > budget.HardMaxRows {
		budget.AgentMaxRows = budget.HardMaxRows
	}
	return budget
}

func configPositiveInt(ctx context.Context, key string, fallback int) int {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil || value.Int() <= 0 {
		return fallback
	}
	return value.Int()
}

func configMissing(ctx context.Context, key string) bool {
	value, err := g.Cfg().Get(ctx, key)
	return err != nil || value == nil || value.Int() <= 0
}

func ReduceRows(rows []map[string]any, maxRows int, maxBytes int, maxSQLTextChars int) ([]map[string]any, evidence.ResultMetadata, error) {
	if maxRows <= 0 {
		return nil, evidence.ResultMetadata{}, fmt.Errorf("max_rows must be positive")
	}
	if maxBytes <= 0 {
		return nil, evidence.ResultMetadata{}, fmt.Errorf("max_bytes must be positive")
	}

	working := cloneRows(rows)
	truncateKnownTextFields(working, maxSQLTextChars)
	metadata := evidence.ResultMetadata{
		MaxRows:  maxRows,
		MaxBytes: maxBytes,
	}
	if len(working) > maxRows {
		working = working[:maxRows]
		metadata.Truncated = true
		metadata.TruncationReason = "max_rows"
	}

	for len(working) > 0 {
		encoded, err := json.Marshal(working)
		if err != nil {
			return nil, evidence.ResultMetadata{}, fmt.Errorf("encode query rows: %w", err)
		}
		if len(encoded) <= maxBytes {
			metadata.ReturnedRows = len(working)
			metadata.ReturnedBytes = len(encoded)
			if len(working) < len(rows) && !metadata.Truncated {
				metadata.Truncated = true
				metadata.TruncationReason = "max_bytes"
			}
			if len(working) < len(rows) && metadata.TruncationReason == "" {
				metadata.TruncationReason = "max_bytes"
			}
			return working, metadata, nil
		}
		working = working[:len(working)-1]
		metadata.Truncated = true
		metadata.TruncationReason = appendReason(metadata.TruncationReason, "max_bytes")
	}

	if len(rows) == 0 {
		return working, metadata, nil
	}
	return nil, evidence.ResultMetadata{}, fmt.Errorf("result_row_exceeds_budget")
}

func cloneRows(rows []map[string]any) []map[string]any {
	if len(rows) == 0 {
		return []map[string]any{}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return append([]map[string]any(nil), rows...)
	}
	var copied []map[string]any
	if err = json.Unmarshal(encoded, &copied); err != nil {
		return append([]map[string]any(nil), rows...)
	}
	return copied
}

func truncateKnownTextFields(rows []map[string]any, maxChars int) {
	if maxChars <= 0 {
		return
	}
	for _, row := range rows {
		for key, value := range row {
			if !isLargeSQLTextField(key) {
				continue
			}
			text, ok := value.(string)
			if !ok || utf8.RuneCountInString(text) <= maxChars {
				continue
			}
			runes := []rune(text)
			row[key] = string(runes[:maxChars])
		}
	}
}

func isLargeSQLTextField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "sql", "sql_text", "sql_digest", "digest_text", "query", "query_text", "statement":
		return true
	default:
		return false
	}
}

func appendReason(existing string, reason string) string {
	if existing == "" {
		return reason
	}
	if strings.Contains(existing, reason) {
		return existing
	}
	return existing + "," + reason
}
