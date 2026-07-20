package indexer

import (
	"SREagent/internal/ai/embedder"
	"SREagent/utility/client"
	"SREagent/utility/common"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	einoindexer "github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type MilvusHybridIndexer struct {
	client    *milvusclient.Client
	embedding embedding.Embedder
}

func NewMilvusIndexer(ctx context.Context) (einoindexer.Indexer, error) {
	knowledgeClient, err := client.NewKnowledgeMilvusClient(ctx)
	if err != nil {
		return nil, err
	}
	if err = client.EnsureKnowledgeCollection(ctx, knowledgeClient); err != nil {
		knowledgeClient.Close(ctx)
		return nil, err
	}
	embeddingModel, err := embedder.DoubaoEmbedding(ctx)
	if err != nil {
		knowledgeClient.Close(ctx)
		return nil, err
	}
	return &MilvusHybridIndexer{client: knowledgeClient, embedding: embeddingModel}, nil
}

func (i *MilvusHybridIndexer) Store(ctx context.Context, docs []*schema.Document, opts ...einoindexer.Option) (ids []string, err error) {
	ctx = callbacks.EnsureRunInfo(ctx, i.GetType(), components.ComponentOfIndexer)
	ctx = callbacks.OnStart(ctx, &einoindexer.CallbackInput{Docs: docs})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	options := einoindexer.GetCommonOptions(&einoindexer.Options{Embedding: i.embedding}, opts...)
	if options.Embedding == nil {
		return nil, fmt.Errorf("knowledge embedding is not configured")
	}
	texts := make([]string, 0, len(docs))
	for _, doc := range docs {
		texts = append(texts, doc.Content)
	}
	vectors, err := options.Embedding.EmbedStrings(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed knowledge documents: %w", err)
	}
	if len(vectors) != len(docs) {
		return nil, fmt.Errorf("knowledge embedding count mismatch: documents=%d vectors=%d", len(docs), len(vectors))
	}

	ids, contents, metadata, denseVectors, err := prepareKnowledgeColumns(ctx, docs, vectors)
	if err != nil {
		return nil, err
	}
	_, err = i.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(common.MilvusCollection(ctx)).WithColumns(
		column.NewColumnVarChar("id", ids),
		column.NewColumnFloatVector("dense_vector", common.MilvusDenseVectorDim(ctx), denseVectors),
		column.NewColumnVarChar("content", contents),
		column.NewColumnJSONBytes("metadata", metadata),
	))
	if err != nil {
		return nil, fmt.Errorf("insert hybrid knowledge documents: %w", err)
	}
	flushTask, err := i.client.Flush(ctx, milvusclient.NewFlushOption(common.MilvusCollection(ctx)))
	if err != nil {
		return nil, fmt.Errorf("flush hybrid knowledge documents: %w", err)
	}
	if err = flushTask.Await(ctx); err != nil {
		return nil, fmt.Errorf("wait for hybrid knowledge flush: %w", err)
	}
	callbacks.OnEnd(ctx, &einoindexer.CallbackOutput{IDs: ids})
	return ids, nil
}

func prepareKnowledgeColumns(ctx context.Context, docs []*schema.Document, vectors [][]float64) ([]string, []string, [][]byte, [][]float32, error) {
	dimension := common.MilvusDenseVectorDim(ctx)
	ids := make([]string, 0, len(docs))
	contents := make([]string, 0, len(docs))
	metadata := make([][]byte, 0, len(docs))
	denseVectors := make([][]float32, 0, len(docs))
	for index, doc := range docs {
		id := doc.ID
		if id == "" {
			id = uuid.NewString()
		}
		if len(vectors[index]) != dimension {
			return nil, nil, nil, nil, fmt.Errorf("knowledge vector dimension mismatch: configured=%d actual=%d", dimension, len(vectors[index]))
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

func (i *MilvusHybridIndexer) GetType() string {
	return "MilvusHybridIndexer"
}

func (i *MilvusHybridIndexer) IsCallbacksEnabled() bool {
	return true
}
