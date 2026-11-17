package knowledge_index_pipeline

import (
	"context"

	"github.com/cloudwego/eino/components/document"
)

// newDocumentTransformer component initialization function of node 'MarkdownSplitter' in graph 'KnowledgeIndexing'
func newDocumentTransformer(ctx context.Context) (tfr document.Transformer, err error) {
	return &contextualTransformer{splitter: &structuralTransformer{limit: 512}}, nil
}
