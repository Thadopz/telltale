package knowledge

import (
	"SREagent/internal/observability"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"go.opentelemetry.io/otel/attribute"
)

const defaultRerankTopN = 3
const dashScopeRerankEndpoint = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

type dashScopeRerankRequest struct {
	Model      string                `json:"model"`
	Input      dashScopeRerankInput  `json:"input"`
	Parameters dashScopeRerankParams `json:"parameters"`
}

type dashScopeRerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type dashScopeRerankParams struct {
	ReturnDocuments bool `json:"return_documents"`
}

type dashScopeRerankResponse struct {
	Output struct {
		Results []struct {
			Index int `json:"index"`
		} `json:"results"`
	} `json:"output"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Rerank(ctx context.Context, query string, documents []*schema.Document) (ranked []*schema.Document, err error) {
	query = strings.TrimSpace(query)
	if query == "" || len(documents) == 0 {
		return documents, nil
	}

	startedAt := time.Now()
	ctx, span := observability.StartSpan(ctx, "knowledge.rerank",
		attribute.String("rerank.provider", "dashscope"),
		attribute.Int("rerank.input_documents", len(documents)),
	)
	defer func() {
		observability.RecordRerank(ctx, time.Since(startedAt), err)
		observability.EndSpan(span, err, attribute.Int("rerank.output_documents", len(ranked)))
	}()

	apiKey, endpoint, err := rerankConfig(ctx)
	if err != nil {
		return nil, err
	}

	contents := make([]string, 0, len(documents))
	for _, document := range documents {
		if document == nil {
			contents = append(contents, "")
			continue
		}
		contents = append(contents, document.Content)
	}

	requestBody := dashScopeRerankRequest{
		Model:      "gte-rerank-v2",
		Input:      dashScopeRerankInput{Query: query, Documents: contents},
		Parameters: dashScopeRerankParams{ReturnDocuments: false},
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-DashScope-DataInspection", "disable")

	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("call rerank service: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("dashscope rerank status=%d body=%s", response.StatusCode, string(responseBody))
	}

	var decoded dashScopeRerankResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	if decoded.Code != "" {
		return nil, fmt.Errorf("dashscope rerank error code=%s message=%s", decoded.Code, decoded.Message)
	}

	ranked = orderDocuments(documents, decoded, rerankTopN(ctx))
	if rerankLogEnabled(ctx) {
		g.Log().Infof(ctx, "[rerank] query=%s original=%v reranked=%v",
			truncateForLog(query, 80), summarizeDocuments(documents), summarizeDocuments(ranked))
	}
	return ranked, nil
}

func orderDocuments(documents []*schema.Document, response dashScopeRerankResponse, topN int) []*schema.Document {
	type scoredDocument struct {
		document *schema.Document
		score    int
	}

	scored := make([]scoredDocument, 0, len(documents))
	for index, document := range documents {
		scored = append(scored, scoredDocument{document: document, score: -index})
	}
	for rank, item := range response.Output.Results {
		if item.Index < 0 || item.Index >= len(scored) {
			continue
		}
		scored[item.Index].score = len(scored) - rank
	}
	sort.SliceStable(scored, func(left int, right int) bool {
		return scored[left].score > scored[right].score
	})

	if len(scored) < topN {
		topN = len(scored)
	}
	ranked := make([]*schema.Document, 0, topN)
	for index := 0; index < topN; index++ {
		ranked = append(ranked, scored[index].document)
	}
	return ranked
}

func rerankConfig(ctx context.Context) (string, string, error) {
	apiKeyValue, err := g.Cfg().Get(ctx, "embedding_model.api_key")
	if err != nil {
		return "", "", err
	}
	apiKey := strings.TrimSpace(apiKeyValue.String())
	if apiKey == "" {
		return "", "", fmt.Errorf("embedding_model.api_key is empty")
	}

	baseURLValue, err := g.Cfg().Get(ctx, "embedding_model.base_url")
	if err != nil {
		return "", "", err
	}
	baseURL := strings.TrimSpace(baseURLValue.String())
	if baseURL == "" {
		return apiKey, dashScopeRerankEndpoint, nil
	}

	trimmed := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/compatible-mode/v1")
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return apiKey, trimmed + "/api/v1/services/rerank/text-rerank/text-rerank", nil
	}
	return apiKey, dashScopeRerankEndpoint, nil
}

func rerankTopN(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "rerank.top_n")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultRerankTopN
	}
	return value.Int()
}

func rerankLogEnabled(ctx context.Context) bool {
	value, err := g.Cfg().Get(ctx, "rerank.observe_log")
	return err == nil && value != nil && value.Bool()
}

func summarizeDocuments(documents []*schema.Document) []string {
	summaries := make([]string, 0, len(documents))
	for index, document := range documents {
		if document == nil {
			summaries = append(summaries, strconv.Itoa(index)+":nil")
			continue
		}
		source := "unknown"
		if rawSource, ok := document.MetaData["_source"]; ok {
			source = truncateForLog(fmt.Sprint(rawSource), 40)
		}
		summaries = append(summaries, strconv.Itoa(index)+":"+source+"#"+truncateForLog(document.Content, 24))
	}
	return summaries
}

func truncateForLog(text string, maxLength int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if maxLength <= 0 || len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "..."
}
