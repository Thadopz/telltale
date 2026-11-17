package tools

import (
	"context"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/knowledge"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type QueryInternalDocsInput struct {
	Mode    string `json:"mode,omitempty" jsonschema:"description=current (default) excludes deprecated planned replaced and out-of-validity documents; history explicitly includes historical and planned documentation."`
	Version string `json:"version,omitempty" jsonschema:"description=Optional exact declared document version. Unknown versions are not inferred."`
	AsOf    string `json:"as_of,omitempty" jsonschema:"description=Optional RFC3339 time for document validity filtering. This does not prove deployment state at that time."`
	Query   string `json:"query" jsonschema:"description=A focused internal documentation query for one unresolved question. Refine it using entities or references found in earlier results; do not repeat an equivalent query."`
}

func NewQueryInternalDocsTool() (tool.InvokableTool, error) {
	t, err := utils.InferOptionableTool(
		"query_internal_docs",
		"Search internal documentation through the shared retrieval and reranking service. Content and runbook_rule contain source chunks. Metadata _context_prefix is AI-generated retrieval context: never quote it as source evidence or treat it as verified runtime state. Inspect content and source metadata, then search again only to resolve a named evidence gap. Results describe documentation, not live system state.",
		func(ctx context.Context, input *QueryInternalDocsInput, opts ...tool.Option) (output string, err error) {
			if input == nil || knowledge.NormalizeQuery(input.Query) == "" {
				return "", fmt.Errorf("knowledge query is required")
			}
			state, _ := ctx.Value(knowledgeSearchKey{}).(*knowledgeSearchState)
			policy := documentpolicy.Options{Mode: input.Mode, Version: input.Version, AsOf: input.AsOf}
			filter, err := policy.Resolve(time.Now())
			if err != nil {
				return "", err
			}
			if state != nil {
				if err := state.beginScoped(input.Query, filter.CacheKey()); err != nil {
					return marshalToolError(err, "knowledge retrieval stopped"), nil
				}
			}
			result, err := knowledge.DefaultService().Search(ctx, knowledge.SearchRequest{
				SessionID: knowledgeToolSessionID(ctx),
				Query:     input.Query,
				Policy:    policy,
			})
			if err != nil {
				return marshalToolError(err, "retrieve internal docs failed"), nil
			}
			message := fmt.Sprintf("retrieved %d internal docs", len(result.Documents))
			if len(result.Documents) == 0 {
				message = "no related internal docs found; this query does not establish that documentation is absent"
			}
			message += fmt.Sprintf("; document scope=%s version=%q; unverified sources need corroboration; planned or historical sources do not prove current implementation or deployment", filter.Mode, filter.Version)
			if result.FromCache {
				message += " from the session query cache"
			}
			if state != nil {
				message += state.observe(result.Documents)
			}
			if len(result.Documents) == 0 {
				return marshalToolData([]any{}, message), nil
			}
			return marshalToolEvidence(result.Documents, message, knowledgeDocumentObservations(result.Documents)), nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func knowledgeDocumentObservations(documents []*schema.Document) []evidence.Observation {
	var observations []evidence.Observation
	for _, document := range documents {
		if document == nil || strings.TrimSpace(document.Content) == "" {
			continue
		}
		observations = append(observations, evidence.Observation{
			Kind: evidence.ObservationRunbookRule,
			Fields: map[string]any{
				"document_id":  document.ID,
				"runbook_rule": document.Content,
				"metadata":     document.MetaData,
			},
		})
	}
	return observations
}

func knowledgeToolSessionID(ctx context.Context) string {
	if sessionID, ok := ctx.Value("client_id").(string); ok {
		return sessionID
	}
	return ""
}
