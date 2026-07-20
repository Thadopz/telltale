package client

import (
	"SREagent/utility/common"
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

const (
	knowledgeDenseField  = "dense_vector"
	knowledgeSparseField = "sparse_vector"
)

func NewKnowledgeMilvusClient(ctx context.Context) (*milvusclient.Client, error) {
	bootstrapClient, err := NewMilvusDatabaseClient(ctx)
	if err != nil {
		return nil, err
	}
	bootstrapClient.Close()

	clientConfig := &milvusclient.ClientConfig{
		Address:  milvusAddress(ctx),
		DBName:   common.MilvusDB(ctx),
		Username: milvusConfigString(ctx, "milvus.username"),
		Password: milvusConfigString(ctx, "milvus.password"),
		APIKey:   milvusConfigString(ctx, "milvus.api_key"),
	}
	knowledgeClient, err := milvusclient.New(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("connect to Milvus knowledge database: %w", err)
	}
	return knowledgeClient, nil
}

func EnsureKnowledgeCollection(ctx context.Context, knowledgeClient *milvusclient.Client) error {
	collectionName := common.MilvusCollection(ctx)
	exists, err := knowledgeClient.HasCollection(ctx, milvusclient.NewHasCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("check knowledge collection: %w", err)
	}
	if exists {
		if err = validateKnowledgeCollection(ctx, knowledgeClient); err != nil {
			return err
		}
		return loadKnowledgeCollection(ctx, knowledgeClient, collectionName)
	}

	schema := entity.NewSchema().
		WithName(collectionName).
		WithDescription("Hybrid business knowledge collection").
		WithField(entity.NewField().
			WithName("id").
			WithDataType(entity.FieldTypeVarChar).
			WithMaxLength(256).
			WithIsPrimaryKey(true)).
		WithField(entity.NewField().
			WithName(knowledgeDenseField).
			WithDataType(entity.FieldTypeFloatVector).
			WithDim(int64(common.MilvusDenseVectorDim(ctx)))).
		WithField(entity.NewField().
			WithName("content").
			WithDataType(entity.FieldTypeVarChar).
			WithMaxLength(8192).
			WithEnableAnalyzer(true).
			WithEnableMatch(true).
			WithAnalyzerParams(map[string]any{"tokenizer": common.MilvusBM25Analyzer(ctx)})).
		WithField(entity.NewField().
			WithName(knowledgeSparseField).
			WithDataType(entity.FieldTypeSparseVector)).
		WithField(entity.NewField().
			WithName("metadata").
			WithDataType(entity.FieldTypeJSON)).
		WithFunction(entity.NewFunction().
			WithName("content_bm25").
			WithType(entity.FunctionTypeBM25).
			WithInputFields("content").
			WithOutputFields(knowledgeSparseField))

	if err = knowledgeClient.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(collectionName, schema)); err != nil {
		return fmt.Errorf("create hybrid knowledge collection: %w", err)
	}
	if err = createKnowledgeIndex(ctx, knowledgeClient, collectionName, knowledgeDenseField, index.NewAutoIndex(entity.COSINE)); err != nil {
		return err
	}
	if err = createKnowledgeIndex(ctx, knowledgeClient, collectionName, knowledgeSparseField, index.NewAutoIndex(entity.BM25)); err != nil {
		return err
	}
	return loadKnowledgeCollection(ctx, knowledgeClient, collectionName)
}

func loadKnowledgeCollection(ctx context.Context, knowledgeClient *milvusclient.Client, collectionName string) error {
	loadState, err := knowledgeClient.GetLoadState(ctx, milvusclient.NewGetLoadStateOption(collectionName))
	if err != nil {
		return fmt.Errorf("get hybrid knowledge collection load state: %w", err)
	}
	if loadState.State == entity.LoadStateLoaded {
		return nil
	}
	loadTask, err := knowledgeClient.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("load hybrid knowledge collection: %w", err)
	}
	if err = loadTask.Await(ctx); err != nil {
		return fmt.Errorf("wait for hybrid knowledge collection load: %w", err)
	}
	return nil
}

func createKnowledgeIndex(ctx context.Context, knowledgeClient *milvusclient.Client, collectionName string, fieldName string, fieldIndex index.Index) error {
	task, err := knowledgeClient.CreateIndex(ctx, milvusclient.NewCreateIndexOption(collectionName, fieldName, fieldIndex))
	if err != nil {
		return fmt.Errorf("create knowledge index for %s: %w", fieldName, err)
	}
	if err = task.Await(ctx); err != nil {
		return fmt.Errorf("wait for knowledge index %s: %w", fieldName, err)
	}
	return nil
}

func validateKnowledgeCollection(ctx context.Context, knowledgeClient *milvusclient.Client) error {
	collectionName := common.MilvusCollection(ctx)
	collection, err := knowledgeClient.DescribeCollection(ctx, milvusclient.NewDescribeCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("describe knowledge collection: %w", err)
	}
	requiredFields := map[string]entity.FieldType{
		"id":                 entity.FieldTypeVarChar,
		knowledgeDenseField:  entity.FieldTypeFloatVector,
		"content":            entity.FieldTypeVarChar,
		knowledgeSparseField: entity.FieldTypeSparseVector,
		"metadata":           entity.FieldTypeJSON,
	}
	for _, field := range collection.Schema.Fields {
		if expectedType, ok := requiredFields[field.Name]; ok && field.DataType == expectedType {
			delete(requiredFields, field.Name)
		}
	}
	if len(requiredFields) > 0 || len(collection.Schema.Functions) == 0 {
		return fmt.Errorf("Milvus collection %q is not Dense+BM25 compatible; use a new milvus.collection name and rebuild the knowledge index", collectionName)
	}
	return nil
}

func milvusConfigString(ctx context.Context, key string) string {
	value, err := g.Cfg().Get(ctx, key)
	if err != nil || value == nil {
		return ""
	}
	return value.String()
}
