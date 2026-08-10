package mem

import (
	"github.com/Thadopz/telltale/internal/ai/embedder"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"context"
	"fmt"
	"math"
	"strings"
)

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
	if err = vectorstore.UpsertDurableMemory(ctx, memory.ID, memory.UserID, memory.MemoryKey, memory.Content, vector); err != nil {
		return err
	}
	return markVectorReady(ctx, memory.ID)
}

func deleteDurableMemoryVector(ctx context.Context, memoryID int64) error {
	return vectorstore.DeleteDurableMemory(ctx, memoryID)
}

func searchDurableMemoryVector(ctx context.Context, userID string, memoryKey string, vector []float32) (float64, bool, error) {
	return vectorstore.SearchDurableMemory(ctx, userID, memoryKey, vector)
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
