package knowledge_index_pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/cloudwego/eino/schema"
	"github.com/gogf/gf/v2/frame/g"
)

func TestHybridChunkingExperiment(t *testing.T) {
	directory := os.Getenv("TELLTALE_CHUNKING_HYBRID")
	if directory == "" {
		t.Skip("requires cached real vectors and explicit isolated directory")
	}
	read := func(name string, value any) {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, value); err != nil {
			t.Fatal(err)
		}
	}
	var groups map[string][]*schema.Document
	var vectors map[string][]float32
	var frozen struct {
		Questions []struct {
			Question string `json:"question"`
		}
	}
	read("chunks.json", &groups)
	read("vectors.json", &vectors)
	read("frozen.json", &frozen)
	var dense []map[string]any
	read("retrievals.json", &dense)
	questionByText := map[string]any{}
	for _, row := range dense {
		question := row["question"].(map[string]any)
		questionByText[question["question"].(string)] = question
	}
	var output []map[string]any
	for _, group := range []string{"A", "B"} {
		if group != os.Getenv("TELLTALE_CHUNKING_GROUP") {
			continue
		}
		path := filepath.Join(directory, "hybrid-"+group+".sqlite")
		if _, err := os.Stat(path); err == nil {
			t.Fatal("isolated experiment database already exists", path)
		}
		t.Setenv("SUPERBIZ_SQLITE_PATH", path)
		if err := storage.Initialize(t.Context()); err != nil {
			t.Fatal(err)
		}
		bySource := map[string][]*schema.Document{}
		for _, doc := range groups[group] {
			bySource[doc.MetaData["_source"].(string)] = append(bySource[doc.MetaData["_source"].(string)], doc)
		}
		for source, docs := range bySource {
			var ids, contents, metadata []string
			var embeddings [][]float32
			for _, doc := range docs {
				encoded, err := json.Marshal(doc.MetaData)
				if err != nil {
					t.Fatal(err)
				}
				id := doc.ID
				if id == "" {
					id = textHash(source + string(encoded) + doc.Content)
				}
				ids = append(ids, id)
				contents = append(contents, doc.Content)
				metadata = append(metadata, string(encoded))
				embeddings = append(embeddings, vectors[textHash(doc.Content)])
			}
			if err := vectorstore.ReplaceKnowledgeSource(t.Context(), source, ids, contents, metadata, embeddings); err != nil {
				t.Fatal(err)
			}
		}
		for _, question := range frozen.Questions {
			started := time.Now()
			docs, err := vectorstore.SearchHybridKnowledge(t.Context(), question.Question, vectors[textHash(question.Question)], 12, 60)
			if err != nil {
				t.Fatal(err)
			}
			var retrieved []map[string]any
			for i, doc := range docs {
				retrieved = append(retrieved, map[string]any{"rank": i + 1, "score": doc.Score(), "document": doc})
			}
			output = append(output, map[string]any{
				"group":     group,
				"retrieval": "hybrid",
				"question":  questionByText[question.Question],
				"retrieved": retrieved,
				"seconds":   time.Since(started).Seconds(),
			})
		}
		if err := g.DB().Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) == 0 {
		t.Fatal("TELLTALE_CHUNKING_GROUP must be A or B")
	}
	if err := os.WriteFile(filepath.Join(directory, "hybrid-"+os.Getenv("TELLTALE_CHUNKING_GROUP")+"-retrievals.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
