package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type corpusEvidence struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}

type corpusGold struct {
	Cases []struct {
		ID    string `json:"id"`
		Split string `json:"split"`
		Turns []struct {
			AnswerType   string     `json:"answer_type"`
			EvidenceSets [][]string `json:"evidence_sets"`
		} `json:"turns"`
	} `json:"cases"`
}

func scoreCorpusRetrieval(output, split string) error {
	if _, err := os.Stat(filepath.Join(output, "retrieval-"+split+".complete")); err != nil {
		return err
	}
	rows, err := readCorpusRetrieval(filepath.Join(output, "retrieval-"+split+".jsonl"))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(corpusDirectory, "assessment", "cases.json"))
	if err != nil {
		return err
	}
	var gold corpusGold
	if err := json.Unmarshal(raw, &gold); err != nil {
		return err
	}
	raw, err = os.ReadFile(filepath.Join(corpusDirectory, "assessment", "evidence-lock.json"))
	if err != nil {
		return err
	}
	var lock struct {
		Evidence map[string]corpusEvidence `json:"evidence"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(output, "retrieval-score-"+split+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, item := range gold.Cases {
		if item.Split != split {
			continue
		}
		for index, turn := range item.Turns {
			row, ok := rows[item.ID][index+1]
			if !ok {
				return fmt.Errorf("missing retrieval for %s turn %d", item.ID, index+1)
			}
			result := map[string]any{"case_id": item.ID, "turn": index + 1, "answer_type": turn.AnswerType}
			for _, k := range []int{5, 10} {
				found := make(map[string]bool)
				for _, doc := range row.Documents[:min(k, len(row.Documents))] {
					source, _ := doc.MetaData["_source"].(string)
					for id, ev := range lock.Evidence {
						if strings.HasSuffix(filepath.ToSlash(source), "/docs/"+ev.Source) && strings.Contains(doc.Content, ev.Quote) {
							found[id] = true
						}
					}
				}
				relevant := make(map[string]bool)
				complete := false
				for _, group := range turn.EvidenceSets {
					all := len(group) > 0
					for _, id := range group {
						if _, ok := lock.Evidence[id]; !ok {
							return fmt.Errorf("unknown evidence %s", id)
						}
						relevant[id] = true
						all = all && found[id]
					}
					complete = complete || all
				}
				matches := make([]string, 0)
				for id := range relevant {
					if found[id] {
						matches = append(matches, id)
					}
				}
				slices.Sort(matches)
				result[fmt.Sprintf("at_%d", k)] = map[string]any{"hit": len(matches) > 0, "matched_evidence": matches, "annotated_evidence": len(relevant), "complete": complete, "interpretation": "exact annotated quotes only; equivalent sources need manual review"}
			}
			if err := encoder.Encode(result); err != nil {
				return err
			}
		}
	}
	return nil
}
