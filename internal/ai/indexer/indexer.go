package indexer

import (
	"github.com/Thadopz/telltale/internal/ai/embedder"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	einoindexer "github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
)

type SQLiteVectorIndexer struct {
	embedding embedding.Embedder
}

func NewSQLiteIndexer(ctx context.Context) (einoindexer.Indexer, error) {
	embeddingModel, err := embedder.DoubaoEmbedding(ctx)
	if err != nil {
		return nil, err
	}
	return &SQLiteVectorIndexer{embedding: embeddingModel}, nil
}

func (i *SQLiteVectorIndexer) Store(ctx context.Context, docs []*schema.Document, opts ...einoindexer.Option) (ids []string, err error) {
	ctx = callbacks.EnsureRunInfo(ctx, i.GetType(), components.ComponentOfIndexer)
	ctx = callbacks.OnStart(ctx, &einoindexer.CallbackInput{Docs: docs})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()
	if len(docs) == 0 {
		callbacks.OnEnd(ctx, &einoindexer.CallbackOutput{IDs: []string{}})
		return []string{}, nil
	}

	options := einoindexer.GetCommonOptions(&einoindexer.Options{Embedding: i.embedding}, opts...)
	if options.Embedding == nil {
		return nil, fmt.Errorf("knowledge embedding is not configured")
	}
	texts := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			return nil, fmt.Errorf("knowledge document is nil")
		}
		texts = append(texts, doc.Content)
	}
	vectors, err := options.Embedding.EmbedStrings(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed knowledge documents: %w", err)
	}
	if len(vectors) != len(docs) {
		return nil, fmt.Errorf("knowledge embedding count mismatch: documents=%d vectors=%d", len(docs), len(vectors))
	}

	ids = make([]string, 0, len(docs))
	contents := make([]string, 0, len(docs))
	metadata := make([]string, 0, len(docs))
	denseVectors := make([][]float32, 0, len(docs))
	for index, doc := range docs {
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		if len(vectors[index]) == 0 {
			return nil, fmt.Errorf("knowledge vector for %s is empty", id)
		}
		metadataJSON, marshalErr := json.Marshal(doc.MetaData)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal knowledge metadata: %w", marshalErr)
		}
		vector := make([]float32, len(vectors[index]))
		for vectorIndex, value := range vectors[index] {
			vector[vectorIndex] = float32(value)
		}
		ids = append(ids, id)
		contents = append(contents, doc.Content)
		metadata = append(metadata, string(metadataJSON))
		denseVectors = append(denseVectors, vector)
	}

	if err = vectorstore.UpsertKnowledgeDocuments(ctx, ids, contents, metadata, denseVectors); err != nil {
		return nil, fmt.Errorf("store sqlite knowledge vectors: %w", err)
	}
	callbacks.OnEnd(ctx, &einoindexer.CallbackOutput{IDs: ids})
	return ids, nil
}

func (i *SQLiteVectorIndexer) GetType() string {
	return "SQLiteVectorIndexer"
}

func (i *SQLiteVectorIndexer) IsCallbacksEnabled() bool {
	return true
}

func prepareKnowledgeColumns(ctx context.Context, docs []*schema.Document, vectors [][]float64) ([]string, []string, [][]byte, [][]float32, error) {
	ids := make([]string, 0, len(docs))
	contents := make([]string, 0, len(docs))
	metadata := make([][]byte, 0, len(docs))
	denseVectors := make([][]float32, 0, len(docs))
	for index, doc := range docs {
		if doc == nil {
			return nil, nil, nil, nil, fmt.Errorf("knowledge document is nil")
		}
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		if len(vectors[index]) == 0 {
			return nil, nil, nil, nil, fmt.Errorf("knowledge vector is empty")
		}
		if len(vectors[index]) != configuredVectorDimension(ctx) {
			return nil, nil, nil, nil, fmt.Errorf("knowledge vector dimension mismatch: configured=%d actual=%d", configuredVectorDimension(ctx), len(vectors[index]))
		}
		metadataJSON, err := json.Marshal(doc.MetaData)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("marshal knowledge metadata: %w", err)
		}
		vector := make([]float32, len(vectors[index]))
		for vectorIndex, value := range vectors[index] {
			vector[vectorIndex] = float32(value)
		}
		ids = append(ids, id)
		contents = append(contents, doc.Content)
		metadata = append(metadata, metadataJSON)
		denseVectors = append(denseVectors, vector)
	}
	return ids, contents, metadata, denseVectors, nil
}

func configuredVectorDimension(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "embedding_model.dimension")
	if err == nil && value != nil && value.Int() > 0 {
		return value.Int()
	}
	return 2048
}
