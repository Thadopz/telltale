package retriever

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
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

const defaultHybridCandidates = 12
const defaultRRFK = 60

type MilvusHybridRetriever struct {
	client    *milvusclient.Client
	embedding embedding.Embedder
	topK      int
	rrfK      float64
}

func NewMilvusRetriever(ctx context.Context) (einoretriever.Retriever, error) {
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
	return &MilvusHybridRetriever{
		client:    knowledgeClient,
		embedding: embeddingModel,
		topK:      hybridCandidates(ctx),
		rrfK:      rrfConstant(ctx),
	}, nil
}

func (r *MilvusHybridRetriever) Retrieve(ctx context.Context, query string, opts ...einoretriever.Option) (docs []*schema.Document, err error) {
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
		Extra: map[string]any{"retrieval": "dense_bm25_rrf", "rrf_k": r.rrfK},
	})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	vectors, err := options.Embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed hybrid knowledge query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("hybrid query embedding count mismatch: got=%d", len(vectors))
	}
	denseVector := make([]float32, len(vectors[0]))
	for index, value := range vectors[0] {
		denseVector[index] = float32(value)
	}
	if len(denseVector) != common.MilvusDenseVectorDim(ctx) {
		return nil, fmt.Errorf("hybrid query vector dimension mismatch: configured=%d actual=%d", common.MilvusDenseVectorDim(ctx), len(denseVector))
	}

	denseRequest := milvusclient.NewAnnRequest("dense_vector", topK, entity.FloatVector(denseVector)).
		WithSearchParam("metric_type", string(entity.COSINE))
	bm25Request := milvusclient.NewAnnRequest("sparse_vector", topK, entity.Text(query)).
		WithSearchParam("metric_type", string(entity.BM25))
	results, err := r.client.HybridSearch(ctx, milvusclient.NewHybridSearchOption(
		common.MilvusCollection(ctx),
		topK,
		denseRequest,
		bm25Request,
	).WithReranker(milvusclient.NewRRFReranker().WithK(r.rrfK)).WithOutputFields("content", "metadata"))
	if err != nil {
		return nil, fmt.Errorf("Milvus Dense+BM25 hybrid search: %w", err)
	}
	docs, err = documentsFromHybridResults(results)
	if err != nil {
		return nil, err
	}
	callbacks.OnEnd(ctx, &einoretriever.CallbackOutput{Docs: docs})
	return docs, nil
}

func documentsFromHybridResults(results []milvusclient.ResultSet) ([]*schema.Document, error) {
	documents := make([]*schema.Document, 0)
	for _, result := range results {
		if result.Err != nil {
			return nil, result.Err
		}
		contentColumn := result.GetColumn("content")
		metadataColumn := result.GetColumn("metadata")
		if result.IDs == nil || contentColumn == nil {
			return nil, fmt.Errorf("hybrid search result is missing id or content")
		}
		for index := 0; index < result.Len(); index++ {
			id, err := result.IDs.GetAsString(index)
			if err != nil {
				return nil, fmt.Errorf("read hybrid search document id: %w", err)
			}
			content, err := contentColumn.GetAsString(index)
			if err != nil {
				return nil, fmt.Errorf("read hybrid search document content: %w", err)
			}
			metadata, err := metadataAt(metadataColumn, index)
			if err != nil {
				return nil, err
			}
			metadata["_retrieval"] = "dense_bm25_rrf"
			if index < len(result.Scores) {
				metadata["_rrf_score"] = float64(result.Scores[index])
			}
			documents = append(documents, (&schema.Document{ID: id, Content: content, MetaData: metadata}).WithScore(float64(scoreAt(result.Scores, index))))
		}
	}
	return documents, nil
}

func metadataAt(metadataColumn interface {
	Get(int) (interface{}, error)
}, index int) (map[string]any, error) {
	metadata := make(map[string]any)
	if metadataColumn == nil {
		return metadata, nil
	}
	raw, err := metadataColumn.Get(index)
	if err != nil {
		return nil, fmt.Errorf("read hybrid search metadata: %w", err)
	}
	metadataJSON, ok := raw.([]byte)
	if !ok || len(metadataJSON) == 0 {
		return metadata, nil
	}
	if err = json.Unmarshal(metadataJSON, &metadata); err != nil {
		return nil, fmt.Errorf("decode hybrid search metadata: %w", err)
	}
	return metadata, nil
}

func scoreAt(scores []float32, index int) float32 {
	if index < 0 || index >= len(scores) {
		return 0
	}
	return scores[index]
}

func hybridCandidates(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "knowledge_retrieval.hybrid_candidates")
	if err != nil || value == nil || value.Int() <= 0 {
		return defaultHybridCandidates
	}
	return value.Int()
}

func rrfConstant(ctx context.Context) float64 {
	value, err := g.Cfg().Get(ctx, "knowledge_retrieval.rrf_k")
	if err != nil || value == nil || value.Float64() <= 0 {
		return defaultRRFK
	}
	return value.Float64()
}

func (r *MilvusHybridRetriever) GetType() string {
	return "MilvusDenseBM25RRFRetriever"
}

func (r *MilvusHybridRetriever) IsCallbacksEnabled() bool {
	return true
}
