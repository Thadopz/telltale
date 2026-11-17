package mem

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/Thadopz/telltale/internal/ai/embedder"
)

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
	var dotProduct, leftLength, rightLength float64
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
