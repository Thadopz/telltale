package mem

import (
	"SREagent/internal/ai/embedder"
	"SREagent/utility/client"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	milvusclient "github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const defaultDurableMemoryCollection = "durable_memories_vector"

func durableMemorySimilarity(ctx context.Context, existing DurableMemory, candidateContent string) (float64, bool) {
	candidateVector, err := embedMemoryContent(ctx, candidateContent)
	if err != nil {
		return 0, false
	}

	if existing.VectorStatus == vectorStatusReady {
		similarity, found, err := searchDurableMemoryVector(ctx, existing.UserID, existing.MemoryKey, candidateVector)
		if err == nil && found {
			return similarity, true
		}
	}

	existingVector, embedErr := embedMemoryContent(ctx, existing.Content)
	if embedErr != nil {
		return 0, false
	}
	return cosineSimilarity(existingVector, candidateVector)
}

func syncDurableMemoryVector(ctx context.Context, memory DurableMemory) error {
	vector, err := embedMemoryContent(ctx, memory.Content)
	if err != nil {
		return err
	}

	milvusClient, err := newDurableMemoryMilvusClient(ctx)
	if err != nil {
		return err
	}
	defer milvusClient.Close()

	collection := durableMemoryCollection(ctx)
	_, err = milvusClient.Upsert(
		ctx,
		collection,
		"",
		entity.NewColumnInt64("memory_id", []int64{memory.ID}),
		entity.NewColumnVarChar("user_id", []string{memory.UserID}),
		entity.NewColumnVarChar("memory_key", []string{memory.MemoryKey}),
		entity.NewColumnVarChar("content", []string{memory.Content}),
		entity.NewColumnVarChar("status", []string{durableMemoryStatusActive}),
		entity.NewColumnFloatVector("vector", len(vector), [][]float32{vector}),
	)
	if err != nil {
		return err
	}
	if err = milvusClient.Flush(ctx, collection, false); err != nil {
		return err
	}
	return markVectorReady(ctx, memory.ID)
}

func deleteDurableMemoryVector(ctx context.Context, memoryID int64) error {
	milvusClient, err := newDurableMemoryMilvusClient(ctx)
	if err != nil {
		return err
	}
	defer milvusClient.Close()

	expression := "memory_id == " + strconv.FormatInt(memoryID, 10)
	return milvusClient.Delete(ctx, durableMemoryCollection(ctx), "", expression)
}

func searchDurableMemoryVector(
	ctx context.Context,
	userID string,
	memoryKey string,
	vector []float32,
) (float64, bool, error) {
	milvusClient, err := newDurableMemoryMilvusClient(ctx)
	if err != nil {
		return 0, false, err
	}
	defer milvusClient.Close()

	searchParam, err := entity.NewIndexAUTOINDEXSearchParam(1)
	if err != nil {
		return 0, false, err
	}

	expression := fmt.Sprintf(
		`user_id == "%s" && memory_key == "%s" && status == "%s"`,
		escapeMilvusString(userID),
		escapeMilvusString(memoryKey),
		durableMemoryStatusActive,
	)
	results, err := milvusClient.Search(
		ctx,
		durableMemoryCollection(ctx),
		[]string{},
		expression,
		[]string{"memory_id"},
		[]entity.Vector{entity.FloatVector(vector)},
		"vector",
		entity.COSINE,
		1,
		searchParam,
	)
	if err != nil {
		return 0, false, err
	}
	if len(results) == 0 || len(results[0].Scores) == 0 {
		return 0, false, nil
	}
	return float64(results[0].Scores[0]), true, nil
}

func newDurableMemoryMilvusClient(ctx context.Context) (milvusclient.Client, error) {
	milvusClient, err := client.NewMilvusDatabaseClient(ctx)
	if err != nil {
		return nil, err
	}
	if err = ensureDurableMemoryCollection(ctx, milvusClient); err != nil {
		milvusClient.Close()
		return nil, err
	}
	return milvusClient, nil
}

func ensureDurableMemoryCollection(ctx context.Context, milvusClient milvusclient.Client) error {
	collection := durableMemoryCollection(ctx)
	collections, err := milvusClient.ListCollections(ctx)
	if err != nil {
		return err
	}

	for _, existing := range collections {
		if existing.Name == collection {
			return milvusClient.LoadCollection(ctx, collection, false)
		}
	}

	dimension := durableMemoryVectorDimension(ctx)
	schema := &entity.Schema{
		CollectionName: collection,
		Description:    "Durable user memory vectors",
		Fields: []*entity.Field{
			{Name: "memory_id", DataType: entity.FieldTypeInt64, PrimaryKey: true},
			{Name: "user_id", DataType: entity.FieldTypeVarChar, TypeParams: map[string]string{"max_length": "128"}},
			{Name: "memory_key", DataType: entity.FieldTypeVarChar, TypeParams: map[string]string{"max_length": "128"}},
			{Name: "content", DataType: entity.FieldTypeVarChar, TypeParams: map[string]string{"max_length": "2048"}},
			{Name: "status", DataType: entity.FieldTypeVarChar, TypeParams: map[string]string{"max_length": "32"}},
			{Name: "vector", DataType: entity.FieldTypeFloatVector, TypeParams: map[string]string{"dim": strconv.Itoa(dimension)}},
		},
	}
	if err = milvusClient.CreateCollection(ctx, schema, entity.DefaultShardNumber); err != nil {
		return err
	}

	vectorIndex, err := entity.NewIndexAUTOINDEX(entity.COSINE)
	if err != nil {
		return err
	}
	if err = milvusClient.CreateIndex(ctx, collection, "vector", vectorIndex, false); err != nil {
		return err
	}
	return milvusClient.LoadCollection(ctx, collection, false)
}

func embedMemoryContent(ctx context.Context, content string) ([]float32, error) {
	memoryEmbedder, err := embedder.DoubaoEmbedding(ctx)
	if err != nil {
		return nil, err
	}
	vectors, err := memoryEmbedder.EmbedStrings(ctx, []string{strings.TrimSpace(content)})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("embedding returned no vector")
	}

	vector := make([]float32, len(vectors[0]))
	for index, value := range vectors[0] {
		vector[index] = float32(value)
	}
	return vector, nil
}

func cosineSimilarity(left []float32, right []float32) (float64, bool) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, false
	}

	var dotProduct float64
	var leftLength float64
	var rightLength float64
	for index := range left {
		leftValue := float64(left[index])
		rightValue := float64(right[index])
		dotProduct += leftValue * rightValue
		leftLength += leftValue * leftValue
		rightLength += rightValue * rightValue
	}
	if leftLength == 0 || rightLength == 0 {
		return 0, false
	}
	return dotProduct / (math.Sqrt(leftLength) * math.Sqrt(rightLength)), true
}

func durableMemoryCollection(ctx context.Context) string {
	value, err := g.Cfg().Get(ctx, "durable_memory.collection")
	if err != nil || value == nil || strings.TrimSpace(value.String()) == "" {
		return defaultDurableMemoryCollection
	}
	return strings.TrimSpace(value.String())
}

func durableMemoryVectorDimension(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "embedding_model.dimension")
	if err != nil || value == nil || value.Int() <= 0 {
		return 2048
	}
	return value.Int()
}

func escapeMilvusString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
