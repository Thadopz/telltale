package tools

import (
	"SREagent/internal/ai/knowledge"
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type QueryInternalDocsInput struct {
	Query string `json:"query" jsonschema:"description=The query string to search in internal documentation for relevant information and processing steps"`
}

func NewQueryInternalDocsTool() (tool.InvokableTool, error) {
	t, err := utils.InferOptionableTool(
		"query_internal_docs",
		"Search internal documentation through the shared Dense + BM25 + RRF + reranker pipeline.",
		func(ctx context.Context, input *QueryInternalDocsInput, opts ...tool.Option) (output string, err error) {
			result, err := knowledge.DefaultService().Search(ctx, knowledge.SearchRequest{
				SessionID: knowledgeToolSessionID(ctx),
				Query:     input.Query,
			})
			if err != nil {
				return marshalToolError(err, "retrieve internal docs failed"), nil
			}
			if len(result.Documents) == 0 {
				return marshalToolData([]any{}, "no related internal docs found"), nil
			}
			message := fmt.Sprintf("retrieved %d internal docs", len(result.Documents))
			if result.FromCache {
				message += " from the session query cache"
			}
			return marshalToolData(result.Documents, message), nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func knowledgeToolSessionID(ctx context.Context) string {
	if sessionID, ok := ctx.Value("client_id").(string); ok {
		return sessionID
	}
	return ""
}
