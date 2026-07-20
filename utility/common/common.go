package common

import (
	"context"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	MilvusDBName         = "agent"
	MilvusCollectionName = "biz_hybrid_v1"
)

var FileDir = "./docs/"

func MilvusDB(ctx context.Context) string {
	v, err := g.Cfg().Get(ctx, "milvus.db_name")
	if err != nil {
		return MilvusDBName
	}
	dbName := strings.TrimSpace(v.String())
	if dbName == "" {
		return MilvusDBName
	}
	return dbName
}

func MilvusDenseVectorDim(ctx context.Context) int {
	value, err := g.Cfg().Get(ctx, "milvus.dense_dimension")
	if err == nil && value != nil && value.Int() > 0 {
		return value.Int()
	}
	value, err = g.Cfg().Get(ctx, "embedding_model.dimension")
	if err == nil && value != nil && value.Int() > 0 {
		return value.Int()
	}
	return 2048
}

func MilvusBM25Analyzer(ctx context.Context) string {
	value, err := g.Cfg().Get(ctx, "milvus.bm25_analyzer")
	if err != nil || value == nil || strings.TrimSpace(value.String()) == "" {
		return "standard"
	}
	return strings.TrimSpace(value.String())
}

func MilvusCollection(ctx context.Context) string {
	v, err := g.Cfg().Get(ctx, "milvus.collection")
	if err != nil {
		return MilvusCollectionName
	}
	collection := strings.TrimSpace(v.String())
	if collection == "" {
		return MilvusCollectionName
	}
	return collection
}
