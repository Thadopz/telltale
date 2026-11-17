package runstate

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/ai/evidence"
	"github.com/gogf/gf/v2/frame/g"
)

type Fact struct {
	EvidenceID string  `json:"evidence_id"`
	Tool       string  `json:"tool"`
	Service    string  `json:"service"`
	Metric     string  `json:"metric"`
	Start      string  `json:"start"`
	End        string  `json:"end"`
	Before     float64 `json:"before"`
	After      float64 `json:"after"`
}

type Check struct {
	Tool          string         `json:"tool"`
	Arguments     map[string]any `json:"arguments"`
	Metric        string         `json:"metric"`
	Direction     string         `json:"direction" jsonschema:"enum=increased,enum=decreased,enum=changed,enum=unchanged,enum=observed,description=For a nonempty metric predict increased or decreased or changed or unchanged. observed requires an empty metric and only records auxiliary query coverage."`
	Discriminates string         `json:"discriminates"`
	Status        string         `json:"status,omitempty"`
}

type Candidate struct {
	ID                  string   `json:"id"`
	Statement           string   `json:"statement"`
	Service             string   `json:"service"`
	Start               string   `json:"start"`
	End                 string   `json:"end"`
	EvidenceIDs         []string `json:"evidence_ids"`
	Checks              []Check  `json:"checks"`
	Status              string   `json:"status,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	OpposingEvidenceIDs []string `json:"opposing_evidence_ids,omitempty"`
}

type Transition struct {
	ID          string   `json:"id"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Reason      string   `json:"reason"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Diagnostic struct {
	MetricTools []string     `json:"metric_tools,omitempty"`
	Transitions []Transition `json:"transitions,omitempty"`
	Candidates  []Candidate  `json:"candidates"`
	Facts       []Fact       `json:"facts"`
	Gaps        []string     `json:"gaps"`
	Conclusion  string       `json:"conclusion"`
	tools       map[string]bool
	metricTools map[string]bool
	checked     map[string]bool
	succeeded   map[string]bool
	references  map[string]bool
	ablation    string
}

func DiagnosticAblation(ctx context.Context) string {
	return g.Cfg().MustGet(ctx, "agent.diagnostic_ablation", "full").String()
}

func EnableDiagnostic(ctx context.Context) {
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		defer state.Unlock()
		if state.diagnostic == nil {
			state.diagnostic = &Diagnostic{
				ablation:    DiagnosticAblation(ctx),
				Conclusion:  "inconclusive",
				tools:       make(map[string]bool),
				metricTools: make(map[string]bool),
				checked:     make(map[string]bool),
				succeeded:   make(map[string]bool),
				references:  make(map[string]bool),
			}
		}
	}
}

func RegisterDiagnosticTools(ctx context.Context, names []string, metricTools ...string) {
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		defer state.Unlock()
		if state.diagnostic != nil {
			for _, name := range names {
				state.diagnostic.tools[name] = true
			}
			for _, name := range metricTools {
				state.diagnostic.metricTools[name] = true
			}
		}
	}
}

func DiagnosticSnapshot(ctx context.Context) *Diagnostic {
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return nil
	}
	state.Lock()
	defer state.Unlock()
	if state.diagnostic == nil {
		return nil
	}
	encoded, _ := json.Marshal(state.diagnostic)
	var result Diagnostic
	_ = json.Unmarshal(encoded, &result)
	result.ablation = state.diagnostic.ablation
	for name := range state.diagnostic.metricTools {
		result.MetricTools = append(result.MetricTools, name)
	}
	slices.Sort(result.MetricTools)
	var retained []Fact
	for _, candidate := range result.Candidates {
		for _, check := range candidate.Checks {
			for i := len(result.Facts) - 1; i >= 0; i-- {
				fact := result.Facts[i]
				if fact.Metric == check.Metric && fact.Service == candidate.Service && fact.Start == candidate.Start && fact.End == candidate.End {
					if !slices.Contains(retained, fact) {
						retained = append(retained, fact)
					}
					break
				}
			}
		}
	}
	for i := len(result.Facts) - 1; i >= 0 && len(retained) < 12; i-- {
		if !slices.Contains(retained, result.Facts[i]) {
			retained = append(retained, result.Facts[i])
		}
	}
	result.Facts = retained
	if len(result.Transitions) > 6 {
		result.Transitions = result.Transitions[len(result.Transitions)-6:]
	}
	return &result
}

func queryKey(tool string, arguments map[string]any) string {
	encoded, _ := json.Marshal(arguments)
	return tool + ":" + string(encoded)
}

// RecordDiagnostic only accepts results after the authorized tool signing boundary.
func RecordDiagnostic(ctx context.Context, tool, arguments string, env evidence.Envelope) {
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return
	}
	state.Lock()
	defer state.Unlock()
	d := state.diagnostic
	if d == nil {
		return
	}
	var input map[string]any
	if json.Unmarshal([]byte(arguments), &input) == nil {
		d.checked[queryKey(tool, input)] = true
		d.succeeded[queryKey(tool, input)] = env.Status == evidence.StatusSuccess && env.DataStatus != evidence.DataUnknown
	}
	if env.Failure != nil && !strings.HasPrefix(env.Failure.Code, "DIAGNOSTIC_") {
		gap := tool + ": " + env.Failure.Code
		if !slices.Contains(d.Gaps, gap) && len(d.Gaps) < 8 {
			d.Gaps = append(d.Gaps, gap)
		}
	}
	if record, usable := env.UsableEvidence(); usable {
		d.references[record.EvidenceID] = true
		for _, observation := range record.Observations {
			if observation.Kind != evidence.ObservationMetricValue {
				continue
			}
			before, beforeOK := observation.Fields["before_mean"].(float64)
			after, afterOK := observation.Fields["after_mean"].(float64)
			metric, _ := observation.Fields["metric"].(string)
			start, e1 := time.Parse(time.RFC3339, observation.Scope.StartTime)
			end, e2 := time.Parse(time.RFC3339, observation.Scope.EndTime)
			if !beforeOK || !afterOK || !finite(before) || !finite(after) || metric == "" || observation.Subject.Service == "" || e1 != nil || e2 != nil || !start.Before(end) {
				continue
			}
			fact := Fact{
				EvidenceID: record.EvidenceID, Tool: tool,
				Service: observation.Subject.Service, Metric: metric,
				Start: start.UTC().Format(time.RFC3339), End: end.UTC().Format(time.RFC3339),
				Before: before, After: after,
			}
			if !slices.Contains(d.Facts, fact) {
				d.Facts = append(d.Facts, fact)
			}
		}
		if len(d.Facts) > 64 {
			d.Facts = d.Facts[len(d.Facts)-64:]
		}
	}
	d.update()
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ProposeDiagnostic(ctx context.Context, candidates []Candidate) error {
	encoded, err := json.Marshal(candidates)
	if err != nil || len(encoded) > 16000 {
		return fmt.Errorf("hypotheses must be bounded JSON")
	}
	var owned []Candidate
	if err := json.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	candidates = owned
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return fmt.Errorf("no diagnostic run")
	}
	state.Lock()
	defer state.Unlock()
	d := state.diagnostic
	if d == nil {
		return fmt.Errorf("diagnostic state is disabled")
	}
	if d.ablation == "single_hypothesis" && len(candidates) != 1 {
		return fmt.Errorf("provide exactly one hypothesis")
	}
	if d.ablation != "single_hypothesis" && (len(candidates) < 2 || len(candidates) > 4) {
		return fmt.Errorf("provide two to four competing hypotheses")
	}
	ids := make(map[string]bool)
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.ID == "" || len(candidate.ID) > 80 || candidate.Statement == "" || len(candidate.Statement) > 500 || candidate.Service == "" || ids[candidate.ID] || len(candidate.EvidenceIDs) == 0 || len(candidate.EvidenceIDs) > 4 || len(candidate.Checks) == 0 || len(candidate.Checks) > 3 {
			return fmt.Errorf("invalid hypothesis identity, evidence or checks")
		}
		ids[candidate.ID] = true
		start, e1 := time.Parse(time.RFC3339, candidate.Start)
		end, e2 := time.Parse(time.RFC3339, candidate.End)
		if e1 != nil || e2 != nil || !start.Before(end) {
			return fmt.Errorf("hypothesis requires an explicit valid RFC3339 window")
		}
		candidate.Start, candidate.End = start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)
		for _, id := range candidate.EvidenceIDs {
			if !d.references[id] {
				return fmt.Errorf("unknown evidence reference %q", id)
			}
		}
		grounded := false
		for _, fact := range d.Facts {
			if fact.Service == candidate.Service && fact.Start == candidate.Start && fact.End == candidate.End && slices.Contains(candidate.EvidenceIDs, fact.EvidenceID) {
				grounded = true
			}
		}
		if !grounded {
			return fmt.Errorf("hypothesis service/window is not grounded in observed metrics")
		}
		for j := range candidate.Checks {
			check := &candidate.Checks[j]
			if !d.tools[check.Tool] || check.Discriminates == "" || len(check.Discriminates) > 300 || !slices.Contains([]string{"increased", "decreased", "unchanged", "changed", "observed"}, check.Direction) {
				return fmt.Errorf("check must name an available tool, scoped metric and discriminating prediction")
			}
			if (check.Metric == "") != (check.Direction == "observed") {
				return fmt.Errorf("check %s: nonempty metric requires direction increased/decreased/changed/unchanged; direction observed is only allowed when metric is empty", check.Metric)
			}
			if check.Metric != "" && !strings.HasPrefix(check.Metric, candidate.Service+"_") {
				return fmt.Errorf("metric check targets a different service")
			}
			if check.Metric != "" && !d.metricTools[check.Tool] {
				return fmt.Errorf("metric predictions require an advertised tool with a name parameter")
			}
			metric, _ := check.Arguments["name"].(string)
			encoded, err := json.Marshal(check.Arguments)
			if err != nil || len(encoded) > 512 {
				return fmt.Errorf("check arguments exceed the bounded query size")
			}
			for key, expected := range map[string]int64{"start": start.Unix(), "end": end.Unix()} {
				if value, exists := check.Arguments[key]; exists && value != float64(expected) {
					return fmt.Errorf("check window must match the hypothesis")
				}
			}
			if check.Metric != "" && metric != check.Metric {
				return fmt.Errorf("check metric must match its name argument")
			}
			check.Status = "pending"
		}
		candidate.Status, candidate.Reason = "open", "prediction not yet checked"
		candidate.OpposingEvidenceIDs = nil
	}
	d.Candidates = candidates
	d.update()
	return nil
}

func (d *Diagnostic) update() {
	supported := 0
	for i := range d.Candidates {
		candidate := &d.Candidates[i]
		previous := candidate.Status
		candidate.Status, candidate.Reason = "open", "prediction not yet checked"
		candidate.OpposingEvidenceIDs = nil
		passed, rejected, metricChecks := 0, 0, 0
		for j := range candidate.Checks {
			check := &candidate.Checks[j]
			check.Status = "pending"
			// A ranking can seed hypotheses; the selected metric must still be queried directly.
			if d.checked[queryKey(check.Tool, check.Arguments)] {
				check.Status = "unknown"
			}
			if d.ablation == "no_feedback" {
				if d.checked[queryKey(check.Tool, check.Arguments)] {
					check.Status = "completed"
				}
				continue
			}
			if check.Direction == "observed" {
				if d.succeeded[queryKey(check.Tool, check.Arguments)] {
					check.Status = "observed"
				}
				continue
			}
			metricChecks++
			for k := len(d.Facts) - 1; k >= 0; k-- {
				fact := d.Facts[k]
				if fact.Metric != check.Metric || fact.Service != candidate.Service || fact.Start != candidate.Start || fact.End != candidate.End || fact.Tool != check.Tool {
					continue
				}
				delta := fact.After - fact.Before
				tolerance := max(math.Abs(fact.Before)*0.1, 1e-12)
				matches := check.Direction == "changed" && math.Abs(delta) > tolerance || check.Direction == "unchanged" && math.Abs(delta) <= tolerance || check.Direction == "increased" && delta > tolerance || check.Direction == "decreased" && delta < -tolerance
				if matches {
					check.Status = "supported"
					passed++
					if !slices.Contains(candidate.EvidenceIDs, fact.EvidenceID) {
						candidate.EvidenceIDs = append(candidate.EvidenceIDs, fact.EvidenceID)
					}
				} else {
					check.Status = "refuted"
					rejected++
					candidate.OpposingEvidenceIDs = append(candidate.OpposingEvidenceIDs, fact.EvidenceID)
				}
				break
			}
		}
		if d.ablation == "no_feedback" {
			candidate.Status, candidate.Reason = "unassessed", "prediction feedback disabled"
		} else if rejected > 0 {
			candidate.Status, candidate.Reason = "refuted", "observed metric contradicts a stated prediction"
		} else if metricChecks > 0 && passed == metricChecks {
			candidate.Status, candidate.Reason = "supported", "metric predictions supported; causality remains unconfirmed"
			supported++
		} else {
			pending := false
			for _, check := range candidate.Checks {
				pending = pending || check.Status == "pending"
			}
			if !pending {
				candidate.Status, candidate.Reason = "unknown", "checks completed without comparable observations for every prediction"
			}
		}
		if previous != candidate.Status {
			d.Transitions = append(d.Transitions, Transition{
				ID: candidate.ID, From: previous, To: candidate.Status,
				Reason: candidate.Reason, EvidenceIDs: append(append([]string(nil), candidate.EvidenceIDs...), candidate.OpposingEvidenceIDs...),
			})
		}
	}
	if len(d.Transitions) > 32 {
		d.Transitions = d.Transitions[len(d.Transitions)-32:]
	}
	d.Conclusion = "inconclusive"
	if d.ablation == "no_feedback" {
		d.Conclusion = "prediction_feedback_disabled"
		return
	}
	if supported == 1 {
		d.Conclusion = "supported_candidate; causality_unconfirmed"
	} else if len(d.Gaps) > 0 && supported == 0 {
		d.Conclusion = "evidence_gap"
	}
}

func DiagnosticReady(ctx context.Context) bool {
	if DiagnosticAblation(ctx) == "free_submit" {
		return true
	}
	d := DiagnosticSnapshot(ctx)
	if d == nil {
		return true
	}
	minimum := 2
	if d.ablation == "single_hypothesis" {
		minimum = 1
	}
	if len(d.Candidates) < minimum {
		return false
	}
	blocked := false
	for _, gap := range d.Gaps {
		for _, code := range []string{": TOOL_BUDGET_EXHAUSTED", ": RESULT_BUDGET_EXCEEDED", ": TIMEOUT", ": PERMISSION_DENIED"} {
			blocked = blocked || strings.Contains(gap, code)
		}
	}
	for _, candidate := range d.Candidates {
		for _, check := range candidate.Checks {
			if check.Status == "pending" && !blocked {
				return false
			}
		}
	}
	return true
}

func DiagnosticQueryAllowed(ctx context.Context, tool, arguments string) bool {
	if DiagnosticAblation(ctx) == "free_query" {
		return true
	}
	d := DiagnosticSnapshot(ctx)
	if d == nil || len(d.Facts) == 0 {
		return true
	}
	for _, metadata := range []string{"describe_case", "list_metrics", "get_current_time"} {
		if tool == metadata || strings.HasSuffix(tool, "__"+metadata) {
			return true
		}
	}
	var input map[string]any
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return false
	}
	for _, candidate := range d.Candidates {
		for _, check := range candidate.Checks {
			if check.Tool == tool && queryKey(tool, check.Arguments) == queryKey(tool, input) {
				return true
			}
			if check.Tool != tool || check.Metric == "" {
				continue
			}
			actual := maps.Clone(input)
			planned := maps.Clone(check.Arguments)
			delete(actual, "points")
			delete(planned, "points")
			validWindow := true
			for key, timestamp := range map[string]string{"start": candidate.Start, "end": candidate.End} {
				if _, pinned := planned[key]; pinned {
					continue
				}
				if value, supplied := actual[key]; supplied {
					parsed, err := time.Parse(time.RFC3339, timestamp)
					if err != nil || value != float64(parsed.Unix()) {
						validWindow = false
					}
					delete(actual, key)
				}
			}
			if validWindow && queryKey(tool, planned) == queryKey(tool, actual) {
				return true
			}
		}
	}
	return false
}

func DiagnosticMemory(d *Diagnostic) string {
	var lines []string
	lines = append(lines, "Task diagnostic state: historical reported observations; revalidate service/window before reuse.")
	for _, candidate := range d.Candidates {
		encoded, _ := json.Marshal(map[string]any{
			"id": candidate.ID, "statement": candidate.Statement, "status": candidate.Status,
			"reason": candidate.Reason, "evidence_ids": candidate.EvidenceIDs,
		})
		lines = append(lines, "hypothesis: "+string(encoded))
	}
	for _, candidate := range d.Candidates {
		for _, fact := range d.Facts {
			if fact.Service == candidate.Service && fact.Start == candidate.Start && fact.End == candidate.End {
				encoded, _ := json.Marshal(fact)
				line := "observation: " + string(encoded)
				if !slices.Contains(lines, line) {
					lines = append(lines, line)
				}
			}
		}
		for _, check := range candidate.Checks {
			if check.Status == "pending" || check.Status == "unknown" {
				encoded, _ := json.Marshal(check)
				lines = append(lines, "pending_check: "+string(encoded))
			}
		}
	}
	return strings.Join(lines, "\n")
}
