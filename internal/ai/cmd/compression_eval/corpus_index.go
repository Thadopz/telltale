package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/agent/knowledge_index_pipeline"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/components/document"
	"github.com/gogf/gf/v2/frame/g"
)

const corpusDirectory = "reports/knowledge-corpus-20261027"

type corpusFile struct {
	Path   string `json:"corpus_path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func frozenCorpusFiles(root string) ([]string, error) {
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var entries []corpusFile
	if err := json.Unmarshal(manifest, &entries); err != nil {
		return nil, err
	}
	if len(entries) != 62 {
		return nil, fmt.Errorf("expected 62 frozen documents, got %d", len(entries))
	}
	docs, err := filepath.Abs(filepath.Join(root, "docs"))
	if err != nil {
		return nil, err
	}
	var files []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(docs, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Ext(path) != ".md" || seen[path] {
			return nil, fmt.Errorf("invalid corpus path %q", entry.Path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.EqualFold(resolved, path) {
			return nil, fmt.Errorf("corpus path contains a link: %q", entry.Path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if len(raw) != entry.Bytes || !strings.EqualFold(hex.EncodeToString(sum[:]), entry.SHA256) {
			return nil, fmt.Errorf("corpus hash mismatch: %s", entry.Path)
		}
		seen[path] = true
		files = append(files, path)
	}
	return files, nil
}

func indexFrozenCorpus(ctx context.Context, output string, contextual, resume bool) error {
	files, err := frozenCorpusFiles(corpusDirectory)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	docs, err := filepath.Abs(filepath.Join(corpusDirectory, "docs"))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(docs, output)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("output must be outside the corpus")
	}
	var completed map[string]bool
	if resume {
		if _, err := os.Stat(filepath.Join(output, "complete")); err == nil {
			return fmt.Errorf("index is already complete")
		}
		if info, err := os.Stat(filepath.Join(output, "experiment.db")); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("isolated index database is missing")
		}
		completed, err = completedCorpusSources(filepath.Join(output, "index.jsonl"), files)
		if err != nil {
			return err
		}
	} else {
		if err := os.Mkdir(output, 0o700); err != nil {
			return fmt.Errorf("output directory must be new: %w", err)
		}
	}
	appconfig.Configure(ctx)
	config, err := g.Cfg().Data(ctx)
	if err != nil {
		return err
	}
	config["knowledge_index"] = map[string]any{"contextual_enabled": contextual}
	setConfig(config, "baseline")
	if err := os.Setenv("SUPERBIZ_SQLITE_PATH", filepath.Join(output, "experiment.db")); err != nil {
		return err
	}
	if err := storage.Initialize(ctx); err != nil {
		return err
	}
	defer g.DB().Close(ctx)
	if resume {
		model, err := g.Cfg().Get(ctx, "quick_chat_model.model")
		if err != nil {
			return err
		}
		endpoint, err := g.Cfg().Get(ctx, "quick_chat_model.base_url")
		if err != nil {
			return err
		}
		for source := range completed {
			raw, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			docs, err := vectorstore.KnowledgeBySource(ctx, source)
			if err != nil || len(docs) == 0 {
				return fmt.Errorf("completed source has no stored chunks: %s", source)
			}
			for _, doc := range docs {
				if doc.MetaData["_source_hash"] != hash(string(raw)) || doc.MetaData["_context_model"] != model.String()+":"+hash(endpoint.String()) {
					return fmt.Errorf("stored chunks do not match source or model: %s", source)
				}
			}
		}
	}
	runner, err := knowledge_index_pipeline.BuildKnowledgeIndexing(ctx)
	if err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if resume {
		flags = os.O_APPEND | os.O_WRONLY
	}
	log, err := os.OpenFile(filepath.Join(output, "index.jsonl"), flags, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	encoder := json.NewEncoder(log)
	for index, path := range files {
		if completed[path] {
			continue
		}
		started := time.Now()
		ids, invokeErr := runner.Invoke(ctx, document.Source{URI: path})
		row := map[string]any{"source": path, "chunks": len(ids), "elapsed_ms": time.Since(started).Milliseconds(), "contextual": contextual}
		if invokeErr != nil {
			row["error"] = invokeErr.Error()
		}
		if err := encoder.Encode(row); err != nil {
			return err
		}
		if err := log.Sync(); err != nil {
			return err
		}
		if invokeErr != nil {
			return fmt.Errorf("index %s: %w", path, invokeErr)
		}
		fmt.Printf("indexed %d/%d %s chunks=%d\n", index+1, len(files), filepath.Base(path), len(ids))
	}
	return os.WriteFile(filepath.Join(output, "complete"), []byte("complete\n"), 0o600)
}

func completedCorpusSources(path string, files []string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	allowed := make(map[string]bool, len(files))
	for _, source := range files {
		allowed[source] = true
	}
	completed := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			Source     string `json:"source"`
			Contextual bool   `json:"contextual"`
			Error      string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		if !allowed[row.Source] || !row.Contextual {
			return nil, fmt.Errorf("index log does not match frozen corpus")
		}
		if row.Error == "" {
			completed[row.Source] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return completed, nil
}
