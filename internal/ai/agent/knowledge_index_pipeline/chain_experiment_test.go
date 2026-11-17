package knowledge_index_pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/embedder"
	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/Thadopz/telltale/internal/ai/knowledge"
	"github.com/Thadopz/telltale/internal/ai/knowledgecompression"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/cloudwego/eino/schema"
)

func TestProductionEmbeddingProbe(t *testing.T) {
	directory := os.Getenv("TELLTALE_CHUNKING_PROBE")
	if directory == "" {
		t.Skip("requires pre-reserved external embedding ledger")
	}
	appconfig.ConfigureAt(t.Context(), "../../../..")
	data, err := os.ReadFile(filepath.Join(directory, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors map[string][]float64
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(directory, "frozen.json"))
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(data, &frozen); err != nil {
		t.Fatal(err)
	}
	text := frozen.Questions[0].Question
	client, err := embedder.DoubaoEmbedding(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := client.EmbedStrings(t.Context(), []string{text})
	if err != nil {
		t.Fatal(err)
	}
	expected := vectors[textHash(text)]
	if len(actual) != 1 || len(actual[0]) != len(expected) {
		t.Fatal("production embedding dimensions differ")
	}
	var dot, left, right float64
	for i, value := range actual[0] {
		dot += value * expected[i]
		left += value * value
		right += expected[i] * expected[i]
	}
	cosine := dot / math.Sqrt(left*right)
	if cosine < 0.999 {
		t.Fatal("production embedding differs from experimental adapter", cosine)
	}
	t.Logf("production embedding probe cosine=%.8f dimensions=%d", cosine, len(expected))
}

type experimentTransport struct {
	base     http.RoundTripper
	path     string
	reserved int
	entries  []map[string]any
}

func (b *experimentTransport) persist() error {
	encoded, err := json.MarshalIndent(map[string]any{"reserved": b.reserved, "requests": b.entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(b.path+".pending", encoded, 0600); err != nil {
		return err
	}
	return os.Rename(b.path+".pending", b.path)
}

func (b *experimentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	if b.reserved+len(body) >= 100000 || len(b.entries) >= 50 {
		return nil, fmt.Errorf("generation/rerank budget exhausted")
	}
	b.reserved += len(body)
	entry := map[string]any{"reserved": len(body), "body_hash": textHash(string(body)), "status": "reserved"}
	b.entries = append(b.entries, entry)
	if err := b.persist(); err != nil {
		return nil, err
	}
	response, err := b.base.RoundTrip(request)
	if err != nil {
		entry["status"] = "failed"
		_ = b.persist()
		return nil, err
	}
	payload, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(payload))
	entry["http_status"] = response.StatusCode
	entry["status"] = "completed"
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		entry["usage"] = decoded["usage"]
	}
	if err := b.persist(); err != nil {
		return nil, err
	}
	return response, nil
}

func TestSmallSampleRealChain(t *testing.T) {
	directory := os.Getenv("TELLTALE_CHUNKING_CHAIN")
	if directory == "" {
		t.Skip("requires completed cached retrieval and explicit live authorization")
	}
	ledger := filepath.Join(directory, "other-budget.json")
	if _, err := os.Stat(ledger); err == nil {
		t.Fatal("chain ledger already exists; preserve completed paid results")
	}
	appconfig.ConfigureAt(t.Context(), "../../../..")
	original := http.DefaultTransport
	budget := &experimentTransport{base: original, path: ledger}
	http.DefaultTransport = budget
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
	data, err := os.ReadFile(filepath.Join(directory, "hybrid-retrievals.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Group     string         `json:"group"`
		Question  map[string]any `json:"question"`
		Retrieved []struct {
			Document *schema.Document `json:"document"`
		} `json:"retrieved"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	selected := map[float64]bool{}
	for _, row := range rows {
		if row.Question["split"] == "holdout" && len(selected) < 4 {
			selected[row.Question["id"].(float64)] = true
		}
	}
	var results []map[string]any
	for _, row := range rows {
		if !selected[row.Question["id"].(float64)] {
			continue
		}
		var documents []*schema.Document
		for _, item := range row.Retrieved {
			documents = append(documents, item.Document)
		}
		question := row.Question["question"].(string)
		ranked, err := knowledge.Rerank(t.Context(), question, documents)
		if err != nil {
			t.Fatal("paid chain stopped", err)
		}
		var observations []evidence.Observation
		var retrieved []map[string]any
		for i, doc := range ranked {
			retrieved = append(retrieved, map[string]any{"rank": i + 1, "score": doc.Score(), "document": doc})
			observations = append(observations, evidence.Observation{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{
				"document_id":  doc.ID,
				"metadata":     doc.MetaData,
				"runbook_rule": doc.Content,
			}})
		}
		envelope := evidence.NewDataWithObservations(nil, "retrieved", observations)
		triggered := knowledgecompression.NeedsSelectionInContext(t.Context(), envelope)
		if triggered {
			t.Fatal("compression requires separate bounded generation reservation")
		}
		_, report, err := knowledgecompression.Select(t.Context(), envelope, knowledgecompression.Goal{Question: question}, "task", nil)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, map[string]any{"group": row.Group, "retrieval": "hybrid-rerank", "question": row.Question,
			"retrieved": retrieved, "compression_triggered": triggered, "compression_report": report})
		encoded, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "chain-retrievals.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
