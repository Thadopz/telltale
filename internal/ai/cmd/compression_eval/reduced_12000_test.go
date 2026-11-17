package main

import (
	"path/filepath"
	"testing"

	"github.com/Thadopz/telltale/internal/ai/evidence"
)

func TestReduced12000PreservesSharedSnapshot(t *testing.T) {
	rows, err := readCorpusRetrieval(filepath.Join("..", "..", "..", "..", "reports", "knowledge-corpus-20261027", "run-20261027-r0", "retrieval-holdout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range reducedCases {
		for _, row := range rows[id] {
			obs := make([]evidence.Observation, 0, len(row.Documents))
			for _, doc := range row.Documents {
				obs = append(obs, evidence.Observation{Kind: evidence.ObservationRunbookRule, Fields: map[string]any{"document_id": doc.ID, "runbook_rule": doc.Content, "metadata": doc.MetaData}})
			}
			raw := evidence.Marshal(evidence.NewDataWithObservations(row.Documents, "retrieved frozen documents", obs))
			for _, policy := range []string{"baseline", "task"} {
				output, _, triggered, err := projectReduced12000(t.Context(), raw, policy, nil, func() error { t.Fatal("unexpected paid selection"); return nil })
				if err != nil {
					t.Fatal(err)
				}
				if triggered {
					t.Fatal("short snapshot triggered selection")
				}
				env := evidence.Parse(output).Envelope
				if env.Evidence == nil || len(env.Evidence.Observations) != len(obs) {
					t.Fatal("snapshot evidence lost")
				}
			}
		}
	}
}
