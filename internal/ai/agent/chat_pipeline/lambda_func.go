package chat_pipeline

import (
	"SREagent/internal/ai/knowledge"
	"SREagent/internal/ai/project_context"
	"SREagent/internal/ai/skills"
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

func newPrepareContextLambda(ctx context.Context, input *UserMessage) (*UserMessage, error) {
	if input == nil {
		return &UserMessage{ModelContext: modelContextPtr(modelContextForInput(ctx, nil))}, nil
	}
	prepared := *input
	modelContext := modelContextForInput(ctx, input)
	prepared.ModelContext = &modelContext
	return &prepared, nil
}

func newContextDocumentsLambda(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
	return newContextDocumentsLambdaWithOptions(ChatAgentOptions{})(ctx, input, opts...)
}

func newContextDocumentsLambdaWithOptions(agentOpts ChatAgentOptions) func(context.Context, *UserMessage, ...any) (map[string]any, error) {
	return func(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
		modelContext := modelContextForInput(ctx, input)
		if !modelContext.UseKnowledge {
			emitRetrieval(ctx, agentOpts, RetrievalObservation{Triggered: false, Query: modelContext.KnowledgeQuery})
			return documentsOutput(nil, "not_requested", knowledge.NormalizeQuery(modelContext.KnowledgeQuery), false), nil
		}

		result, err := knowledge.DefaultService().Search(ctx, knowledge.SearchRequest{
			SessionID:  knowledgeSessionID(ctx, input),
			Query:      modelContext.KnowledgeQuery,
			Retriever:  agentOpts.Retriever,
			SkipRerank: agentOpts.SkipRerank,
		})
		if err != nil {
			g.Log().Warningf(ctx, "context knowledge retrieval failed, err=%v", err)
			emitRetrieval(ctx, agentOpts, RetrievalObservation{Triggered: true, Query: modelContext.KnowledgeQuery, Error: err.Error()})
			return documentsOutput(knowledgeFailureDocuments("knowledge retrieval failed", err), "failed", knowledge.NormalizeQuery(modelContext.KnowledgeQuery), false), nil
		}

		budgeted := budgetDocuments(result.Documents, agentOpts.documentsMax(), agentOpts.documentMax())
		if result.RerankError != nil {
			budgeted = append(budgeted, knowledgeFailureDocuments("knowledge rerank failed; using original retrieval order", result.RerankError)...)
		}
		stage := "reranked"
		if agentOpts.SkipRerank {
			stage = "retrieved"
		}
		if result.FromCache {
			stage = "cache"
		}
		emitRetrieval(ctx, agentOpts, RetrievalObservation{
			Triggered: true,
			Query:     modelContext.KnowledgeQuery,
			Docs:      documentEvidence(budgeted, stage),
		})
		return documentsOutput(budgeted, "success", result.NormalizedQuery, result.FromCache), nil
	}
}

// newInputToChatLambda component initialization function of node 'InputToHistory' in graph 'EinoAgent'
func newInputToChatLambda(ctx context.Context, input *UserMessage, opts ...any) (output map[string]any, err error) {
	modelContext := modelContextForInput(ctx, input)
	modelContext.ProjectContext = project_context.Load(ctx)
	modelContext.Skills = skills.LoadSelectedContext(ctx, modelContext.Content)
	return modelContext.PromptValues(), nil
}

func modelContextForInput(ctx context.Context, input *UserMessage) ModelContext {
	if input != nil && input.ModelContext != nil {
		return *input.ModelContext
	}
	return AssembleModelContext(ctx, input, time.Now())
}

func modelContextPtr(modelContext ModelContext) *ModelContext {
	return &modelContext
}

func documentsOutput(docs []*schema.Document, status string, normalizedQuery string, cacheHit bool) map[string]any {
	if docs == nil {
		docs = []*schema.Document{}
	}
	return map[string]any{
		"documents":                     docs,
		"knowledge_retrieval_status":    status,
		"knowledge_normalized_query":    normalizedQuery,
		"knowledge_retrieval_cache_hit": cacheHit,
	}
}

func knowledgeSessionID(ctx context.Context, input *UserMessage) string {
	if input != nil && input.ConversationID != "" {
		return input.ConversationID
	}
	if sessionID, ok := ctx.Value("client_id").(string); ok {
		return sessionID
	}
	return ""
}

func knowledgeFailureDocuments(message string, err error) []*schema.Document {
	content := message
	if err != nil {
		content = fmt.Sprintf("%s: %v", message, err)
	}
	return []*schema.Document{{
		Content: "Knowledge retrieval status: " + content + ". Do not conclude that runbooks or internal documents are absent.",
		MetaData: map[string]any{
			"_source": "knowledge_retrieval_status",
			"status":  "error",
		},
	}}
}
