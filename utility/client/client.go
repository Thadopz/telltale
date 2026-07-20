package client

import (
	"SREagent/utility/common"
	"context"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	cli "github.com/milvus-io/milvus-sdk-go/v2/client"
)

func NewMilvusClient(ctx context.Context) (cli.Client, error) {
	collectionName := common.MilvusCollection(ctx)
	knowledgeClient, err := NewKnowledgeMilvusClient(ctx)
	if err != nil {
		return nil, err
	}
	if err = EnsureKnowledgeCollection(ctx, knowledgeClient); err != nil {
		knowledgeClient.Close(ctx)
		return nil, err
	}
	knowledgeClient.Close(ctx)

	agentClient, err := NewMilvusDatabaseClient(ctx)
	if err != nil {
		return nil, err
	}

	if err = agentClient.LoadCollection(ctx, collectionName, false); err != nil {
		agentClient.Close()
		return nil, fmt.Errorf("failed to load collection: %w", err)
	}

	return agentClient, nil
}

func NewMilvusDatabaseClient(ctx context.Context) (cli.Client, error) {
	address := milvusAddress(ctx)
	dbName := common.MilvusDB(ctx)
	defaultClient, err := cli.NewClient(ctx, cli.Config{
		Address: address,
		DBName:  "default",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to default database: %w", err)
	}

	databases, err := defaultClient.ListDatabases(ctx)
	if err != nil {
		defaultClient.Close()
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	agentDBExists := false
	for _, db := range databases {
		if db.Name == dbName {
			agentDBExists = true
			break
		}
	}
	if !agentDBExists {
		err = defaultClient.CreateDatabase(ctx, dbName)
		if err != nil {
			defaultClient.Close()
			return nil, fmt.Errorf("failed to create agent database: %w", err)
		}
	}
	defaultClient.Close()

	agentClient, err := cli.NewClient(ctx, cli.Config{
		Address: address,
		DBName:  dbName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to agent database: %w", err)
	}
	return agentClient, nil
}

func milvusAddress(ctx context.Context) string {
	v, err := g.Cfg().Get(ctx, "milvus.address")
	if err != nil {
		return "localhost:19530"
	}
	address := strings.TrimSpace(v.String())
	if address == "" {
		return "localhost:19530"
	}
	return address
}
