package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssessmentInputs(t *testing.T) {
	inputs, err := loadDatasetInputs(filepath.Join("..", "..", "..", "..", "reports", "knowledge-corpus-20261027", "assessment", "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	turns := 0
	for _, input := range inputs {
		counts[input.Split]++
		turns += len(input.Turns)
	}
	if len(inputs) != 32 || counts["dev"] != 8 || counts["holdout"] != 24 || turns != 38 {
		t.Fatalf("unexpected dataset: inputs=%d turns=%d splits=%v", len(inputs), turns, counts)
	}
}

func TestFrozenCorpusManifest(t *testing.T) {
	files, err := frozenCorpusFiles(filepath.Join("..", "..", "..", "..", "reports", "knowledge-corpus-20261027"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 62 {
		t.Fatalf("unexpected corpus size: %d", len(files))
	}
}

func TestIndexRejectsOutputInsideCorpus(t *testing.T) {
	// Paths used by the command are relative to the project root.
	root := filepath.Join("..", "..", "..", "..")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := indexFrozenCorpus(t.Context(), filepath.Join(corpusDirectory, "docs", "forbidden-output"), true, false); err == nil {
		t.Fatal("accepted an output directory inside the indexed corpus")
	}
}

func TestAssessmentInputRejectsAnswersAndTrailingJSON(t *testing.T) {
	for _, content := range []string{
		`{"id":"D01","split":"dev","turns":[{"user":"question"}],"reference_answer":"answer"}`,
		`{"id":"D01","split":"dev","turns":[{"user":"question"}]} {}`,
	} {
		path := filepath.Join(t.TempDir(), "inputs.jsonl")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadDatasetInputs(path); err == nil {
			t.Fatalf("accepted invalid input: %s", content)
		}
	}
}
