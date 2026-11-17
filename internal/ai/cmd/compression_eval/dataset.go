package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// DatasetInput contains only information allowed in the evaluated model's input.
type DatasetInput struct {
	ID    string        `json:"id"`
	Split string        `json:"split"`
	Turns []DatasetTurn `json:"turns"`
}

type DatasetTurn struct {
	User string `json:"user"`
}

func loadDatasetInputs(path string) ([]DatasetInput, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var inputs []DatasetInput
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var input DatasetInput
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("input line %d: %w", len(inputs)+1, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("input line %d contains trailing JSON", len(inputs)+1)
		}
		if input.ID == "" || seen[input.ID] || (input.Split != "dev" && input.Split != "holdout") || len(input.Turns) == 0 {
			return nil, fmt.Errorf("invalid input line %d", len(inputs)+1)
		}
		for _, turn := range input.Turns {
			if strings.TrimSpace(turn.User) == "" {
				return nil, fmt.Errorf("empty question in %s", input.ID)
			}
		}
		seen[input.ID] = true
		inputs = append(inputs, input)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("input dataset is empty")
	}
	return inputs, nil
}
