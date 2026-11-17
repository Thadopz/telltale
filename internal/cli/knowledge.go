package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/Thadopz/telltale/internal/ai/documentpolicy"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/agent/knowledge_index_pipeline"
	retriever "github.com/Thadopz/telltale/internal/ai/retriever"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/components/document"
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/gogf/gf/v2/frame/g"
)

// RunKnowledge is shared by telltale knowledge and the historical knowledge
// and recall commands.
func RunKnowledge(ctx context.Context, args []string) error {
	configDir, commandArgs, err := splitConfigDir(args)
	if err != nil {
		return usageError(err)
	}
	return runKnowledge(ctx, commandArgs, configDir, os.Stdout)
}

func runKnowledge(ctx context.Context, args []string, configDir string, output io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printKnowledgeUsage(os.Stderr)
		return nil
	}
	if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		if args[0] == "index" {
			fmt.Fprintln(os.Stderr, "Usage: telltale knowledge index [--docs DIR]")
		} else if args[0] == "recall" {
			fmt.Fprintln(os.Stderr, "Usage: telltale knowledge recall --query TEXT [--limit N] [--mode current|history] [--version VERSION] [--as-of RFC3339]")
		} else if args[0] == "annotate" {
			fmt.Fprintln(os.Stderr, "Usage: telltale knowledge annotate --source SOURCE [--metadata FILE]")
		}
		return nil
	}
	ctx = quietCommandContext(ctx)
	configure(ctx, configDir)
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		return runtimeError(err)
	}
	if err := storage.Initialize(ctx); err != nil {
		return runtimeError(err)
	}

	switch args[0] {
	case "index":
		return runKnowledgeIndex(ctx, args[1:], output)
	case "recall":
		return runKnowledgeRecall(ctx, args[1:], output)
	case "annotate":
		return runKnowledgeAnnotate(ctx, args[1:], output)
	default:
		return usageError(fmt.Errorf("unsupported knowledge command %q; use index, recall or annotate", args[0]))
	}
}

func runKnowledgeIndex(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("telltale knowledge index", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	docsDir := flags.String("docs", "", "directory containing markdown documents")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 {
		return usageError(fmt.Errorf("unexpected argument %q", flags.Args()[0]))
	}

	directory := strings.TrimSpace(*docsDir)
	if directory == "" {
		directory = "data/docs"
		if value, err := g.Cfg().Get(ctx, "file_dir"); err == nil && value != nil {
			configured := strings.TrimSpace(value.String())
			if configured != "" && !strings.Contains(configured, "YOUR_") {
				directory = configured
			}
		}
	}
	runner, err := knowledge_index_pipeline.BuildKnowledgeIndexing(ctx)
	if err != nil {
		return runtimeError(err)
	}

	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		fmt.Fprintf(output, "[start] indexing file: %s\n", path)
		ids, invokeErr := runner.Invoke(ctx, document.Source{URI: path})
		if invokeErr != nil {
			return fmt.Errorf("index %s: %w", path, invokeErr)
		}
		fmt.Fprintf(output, "[done] indexing file: %s, parts: %d\n", path, len(ids))
		return nil
	})
	if err != nil {
		return runtimeError(err)
	}
	return nil
}

func runKnowledgeRecall(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("telltale knowledge recall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	query := flags.String("query", "服务下线是什么原因", "knowledge query")
	limit := flags.Int("limit", 0, "maximum number of documents")
	mode := flags.String("mode", "current", "current or history document scope")
	version := flags.String("version", "", "exact declared document version")
	asOf := flags.String("as-of", "", "RFC3339 document validity time")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 {
		return usageError(fmt.Errorf("unexpected argument %q", flags.Args()[0]))
	}
	if strings.TrimSpace(*query) == "" {
		return usageError(fmt.Errorf("--query is required"))
	}

	filter, err := (documentpolicy.Options{Mode: *mode, Version: *version, AsOf: *asOf}).Resolve(time.Now())
	if err != nil {
		return usageError(err)
	}
	ctx = documentpolicy.WithFilter(ctx, filter)
	knowledgeRetriever, err := retriever.NewSQLiteRetriever(ctx)
	if err != nil {
		return runtimeError(err)
	}
	options := make([]einoretriever.Option, 0, 1)
	if *limit > 0 {
		options = append(options, einoretriever.WithTopK(*limit))
	}
	docs, err := knowledgeRetriever.Retrieve(ctx, strings.TrimSpace(*query), options...)
	if err != nil {
		return runtimeError(err)
	}
	for _, doc := range docs {
		if doc != nil {
			record, _ := documentpolicy.FromMetadata(doc.MetaData)
			fmt.Fprintf(output, "[document state=%s version=%q source=%v; documentation does not prove live state]\n", record.State, record.Version, doc.MetaData["_source"])
			fmt.Fprintln(output, doc.Content)
		}
	}
	fmt.Fprintf(output, "Done %d\n", len(docs))
	return nil
}

func printKnowledgeUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: telltale knowledge index [--docs DIR]")
	fmt.Fprintln(output, "       telltale knowledge recall --query TEXT [--limit N] [--mode current|history] [--version VERSION] [--as-of RFC3339]")
	fmt.Fprintln(output, "       telltale knowledge annotate --source SOURCE [--metadata FILE]")
}

func runKnowledgeAnnotate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("telltale knowledge annotate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	source := flags.String("source", "", "exact indexed source identifier")
	metadata := flags.String("metadata", "", "metadata JSON file; defaults to SOURCE.meta.json")
	if err := flags.Parse(args); err != nil {
		return usageError(err)
	}
	if len(flags.Args()) > 0 || strings.TrimSpace(*source) == "" {
		return usageError(fmt.Errorf("--source is required; positional arguments are not accepted"))
	}
	path := *metadata
	if path == "" {
		path = *source + ".meta.json"
	}
	file, err := os.Open(path)
	if err != nil {
		return runtimeError(err)
	}
	defer file.Close()
	record, err := documentpolicy.Decode(file)
	if err != nil {
		return usageError(err)
	}
	count, err := vectorstore.UpdateKnowledgePolicy(ctx, *source, record)
	if err != nil {
		return runtimeError(err)
	}
	fmt.Fprintf(output, "Updated policy for %d indexed chunks; source text and embeddings retained.\n", count)
	return nil
}
