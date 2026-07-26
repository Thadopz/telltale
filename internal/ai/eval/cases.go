package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func LoadCases(path string) ([]Case, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return loadCasesFile(path)
	}

	var paths []string
	err = filepath.WalkDir(path, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			return nil
		}
		paths = append(paths, currentPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var cases []Case
	seenIDs := map[string]string{}
	for _, casePath := range paths {
		fileCases, loadErr := loadCasesFile(casePath)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, evalCase := range fileCases {
			if previousPath, exists := seenIDs[evalCase.ID]; exists {
				return nil, fmt.Errorf("duplicate eval case id %q in %s and %s", evalCase.ID, previousPath, casePath)
			}
			seenIDs[evalCase.ID] = casePath
			cases = append(cases, evalCase)
		}
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no eval cases found in %s", path)
	}
	return cases, nil
}

func loadCasesFile(path string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cases []Case
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var evalCase Case
		if err = json.Unmarshal([]byte(line), &evalCase); err != nil {
			return nil, fmt.Errorf("%s:%d invalid jsonl case: %w", path, lineNumber, err)
		}
		if err = validateCase(evalCase); err != nil {
			return nil, fmt.Errorf("%s:%d %w", path, lineNumber, err)
		}
		cases = append(cases, evalCase)
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

func validateCase(evalCase Case) error {
	if strings.TrimSpace(evalCase.ID) == "" {
		return fmt.Errorf("missing id")
	}
	if strings.TrimSpace(evalCase.Query) == "" {
		return fmt.Errorf("case %q is missing query", evalCase.ID)
	}

	fixtureNames := map[string]bool{}
	for _, fixture := range evalCase.ToolFixtures {
		name := strings.TrimSpace(fixture.Name)
		if name == "" {
			return fmt.Errorf("case %q has a tool fixture without a name", evalCase.ID)
		}
		if fixtureNames[name] {
			return fmt.Errorf("case %q has duplicate tool fixture %q", evalCase.ID, name)
		}
		fixtureNames[name] = true
	}

	if err := validateModelSteps(evalCase.ID, "model_steps", evalCase.ModelSteps, fixtureNames); err != nil {
		return err
	}
	if err := validateModelSteps(evalCase.ID, "failover_model_steps", evalCase.FailoverModelSteps, fixtureNames); err != nil {
		return err
	}
	return nil
}

func validateModelSteps(caseID string, field string, steps []ModelStep, fixtureNames map[string]bool) error {
	for index, step := range steps {
		switch step.Type {
		case "tool_call":
			if strings.TrimSpace(step.Tool) == "" {
				return fmt.Errorf("case %q %s step %d is missing tool", caseID, field, index+1)
			}
			if !fixtureNames[step.Tool] {
				return fmt.Errorf("case %q %s step %d references missing tool fixture %q", caseID, field, index+1, step.Tool)
			}
		case "answer":
		case "model_error":
			if strings.TrimSpace(step.Error) == "" {
				return fmt.Errorf("case %q %s step %d is missing error", caseID, field, index+1)
			}
		default:
			return fmt.Errorf("case %q %s step %d has unsupported type %q", caseID, field, index+1, step.Type)
		}
	}
	return nil
}
