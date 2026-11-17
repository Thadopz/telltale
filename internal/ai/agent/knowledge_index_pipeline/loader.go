package knowledge_index_pipeline

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/cloudwego/eino/schema"

	"github.com/cloudwego/eino-ext/components/document/loader/file"
	"github.com/cloudwego/eino/components/document"
)

// newLoader component initialization function of node 'FileLoader' in graph 'KnowledgeIndexing'
func newLoader(ctx context.Context) (ldr document.Loader, err error) {
	// TODO Modify component configuration here.
	config := &file.FileLoaderConfig{}
	ldr, err = file.NewFileLoader(ctx, config)
	if err != nil {
		return nil, err
	}
	return &policyLoader{Loader: ldr}, nil
}

type policyLoader struct{ document.Loader }

func (l *policyLoader) Load(ctx context.Context, source document.Source, opts ...document.LoaderOption) ([]*schema.Document, error) {
	record := documentpolicy.Record{State: "unverified"}
	file, err := os.Open(source.URI + ".meta.json")
	if err == nil {
		defer file.Close()
		record, err = documentpolicy.Decode(file)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("document metadata %s: %w", source.URI, err)
	}
	docs, err := l.Loader.Load(ctx, source, opts...)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		doc.MetaData = maps.Clone(doc.MetaData)
		if doc.MetaData == nil {
			doc.MetaData = map[string]any{}
		}
		doc.MetaData[documentpolicy.MetadataKey] = record
	}
	return docs, nil
}
