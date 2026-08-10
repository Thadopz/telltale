package knowledge_index_pipeline

import (
	"context"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/compose"
)

func BuildKnowledgeIndexing(ctx context.Context) (r compose.Runnable[document.Source, []string], err error) {
	const (
		FileLoader       = "FileLoader"
		MarkdownSplitter = "MarkdownSplitter"
		SQLiteIndexer    = "SQLiteIndexer"
	)
	g := compose.NewGraph[document.Source, []string]()
	fileLoaderKeyOfLoader, err := newLoader(ctx)
	if err != nil {
		return nil, err
	}
	_ = g.AddLoaderNode(FileLoader, fileLoaderKeyOfLoader)
	markdownSplitterKeyOfDocumentTransformer, err := newDocumentTransformer(ctx)
	if err != nil {
		return nil, err
	}
	_ = g.AddDocumentTransformerNode(MarkdownSplitter, markdownSplitterKeyOfDocumentTransformer)
	sqliteIndexerKeyOfIndexer, err := newIndexer(ctx)
	if err != nil {
		return nil, err
	}
	_ = g.AddIndexerNode(SQLiteIndexer, sqliteIndexerKeyOfIndexer)
	_ = g.AddEdge(compose.START, FileLoader)
	_ = g.AddEdge(SQLiteIndexer, compose.END)
	_ = g.AddEdge(FileLoader, MarkdownSplitter)
	_ = g.AddEdge(MarkdownSplitter, SQLiteIndexer)
	r, err = g.Compile(ctx, compose.WithGraphName("KnowledgeIndexing"), compose.WithNodeTriggerMode(compose.AnyPredecessor))
	if err != nil {
		return nil, err
	}
	return r, err
}
