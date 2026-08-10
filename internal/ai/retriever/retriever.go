package retriever

import (
	"github.com/Thadopz/telltale/internal/ai/embedder"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"context"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const defaultSQLiteCandidates = 12

type SQLiteVectorRetriever struct {
	embedding embedding.Embedder
	topK      int
}

func NewSQLiteRetriever(ctx context.Context) (einoretriever.Retriever, error) {
	embeddingModel, err := embedder.DoubaoEmbedding(ctx)
	if err != nil {
		return nil, err
	}
	return &SQLiteVectorRetriever{
		embedding: embeddingModel,
		topK:      configuredCandidates(ctx),
	}, nil
}

func (r *SQLiteVectorRetriever) Retrieve(ctx context.Context, query string, opts ...einoretriever.Option) (docs []*schema.Document, err error) {
	options := einoretriever.GetCommonOptions(&einoretriever.Options{
		TopK:      &r.topK,
		Embedding: r.embedding,
	}, opts...)
	if options.Embedding == nil {
		return nil, fmt.Errorf("knowledge embedding is not configured")
	}
	topK := r.topK
	if options.TopK != nil && *options.TopK > 0 {
		topK = *options.TopK
	}

	ctx = callbacks.EnsureRunInfo(ctx, r.GetType(), components.ComponentOfRetriever)
	ctx = callbacks.OnStart(ctx, &einoretriever.CallbackInput{
		Query: query,
		TopK:  topK,
		Extra: map[string]any{"retrieval": "sqlite_cosine"},
	})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	vectors, err := options.Embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed sqlite knowledge query: %w", err)
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("knowledge query embedding count mismatch")
	}
	queryVector := make([]float32, len(vectors[0]))
	for index, value := range vectors[0] {
		queryVector[index] = float32(value)
	}
	docs, err = vectorstore.SearchKnowledge(ctx, queryVector, topK)
	if err != nil {
		return nil, fmt.Errorf("search sqlite knowledge vectors: %w", err)
	}
	callbacks.OnEnd(ctx, &einoretriever.CallbackOutput{Docs: docs})
	return docs, nil
}

func (r *SQLiteVectorRetriever) GetType() string {
	return "SQLiteCosineRetriever"
}

func (r *SQLiteVectorRetriever) IsCallbacksEnabled() bool {
	return true
}

func configuredCandidates(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "knowledge_retrieval.hybrid_candidates")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultSQLiteCandidates
	}
	return value.Int()
}
