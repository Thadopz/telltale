package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Thadopz/telltale/internal/ai/knowledge"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

func retrieveFrozenCorpus(ctx context.Context, output, inputPath, split string) error {
	if _, err := frozenCorpusFiles(corpusDirectory); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(output, "complete")); err != nil {
		return fmt.Errorf("index is incomplete: %w", err)
	}
	if split == "holdout" {
		if _, err := os.Stat(filepath.Join(output, "retrieval-dev.complete")); err != nil {
			return fmt.Errorf("development retrieval is incomplete: %w", err)
		}
	}
	inputs, err := loadDatasetInputs(inputPath)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	database := filepath.Join(output, "experiment.db")
	if info, err := os.Stat(database); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("isolated index database is missing")
	}
	appconfig.Configure(ctx)
	if err := os.Setenv("SUPERBIZ_SQLITE_PATH", database); err != nil {
		return err
	}
	if err := storage.Initialize(ctx); err != nil {
		return err
	}
	defer g.DB().Close(ctx)
	file, err := os.OpenFile(filepath.Join(output, "retrieval-"+split+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	service := knowledge.NewService()
	for _, input := range inputs {
		if input.Split != split {
			continue
		}
		for turn, question := range input.Turns {
			started := time.Now()
			result, searchErr := service.Search(ctx, knowledge.SearchRequest{Query: question.User, SkipRerank: true})
			row := map[string]any{"case_id": input.ID, "split": split, "turn": turn + 1, "query": question.User, "documents": result.Documents, "elapsed_ms": time.Since(started).Milliseconds(), "cache_hit": result.FromCache, "rerank": "disabled", "embedding_usage": "unavailable", "retrieval_strategy": "sqlite_bm25_dense_rrf"}
			if searchErr != nil {
				row["error"] = searchErr.Error()
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			if searchErr != nil {
				return fmt.Errorf("retrieve %s turn %d: %w", input.ID, turn+1, searchErr)
			}
			fmt.Printf("retrieved %s turn=%d documents=%d\n", input.ID, turn+1, len(result.Documents))
		}
	}
	return os.WriteFile(filepath.Join(output, "retrieval-"+split+".complete"), []byte("complete\n"), 0o600)
}
