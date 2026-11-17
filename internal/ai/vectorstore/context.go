package vectorstore

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

const ContextPrefixKey = "_context_prefix"

func EmbeddingInputLimit(ctx context.Context) (int, error) {
	value, err := g.Cfg().Get(ctx, "embedding_model.max_input_tokens")
	if err != nil {
		return 0, err
	}
	if value == nil || value.IsNil() {
		return 2048, nil
	}
	if value.Int() < 1024 {
		return 0, fmt.Errorf("embedding_model.max_input_tokens must be at least 1024")
	}
	return value.Int(), nil
}

// RetrievalText keeps generated context separate from the source quotation.
func RetrievalText(doc *schema.Document) string {
	if prefix, ok := doc.MetaData[ContextPrefixKey].(string); ok && prefix != "" {
		return prefix + "\n\n" + doc.Content
	}
	return doc.Content
}
