package trace

import (
	"SREagent/internal/ai/evidence"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
)

type AgentStep struct {
	Index     int       `json:"index"`
	Agent     string    `json:"agent"`
	Phase     string    `json:"phase"`
	Component string    `json:"component,omitempty"`
	Type      string    `json:"type,omitempty"`
	Name      string    `json:"name,omitempty"`
	Input     string    `json:"input,omitempty"`
	Output    string    `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	ElapsedMS int64     `json:"elapsed_ms,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ToolEvidence struct {
	ToolName      string            `json:"tool_name"`
	InputJSON     string            `json:"input_json,omitempty"`
	OutputJSON    string            `json:"output_json,omitempty"`
	Envelope      evidence.Envelope `json:"envelope"`
	EvidenceID    string            `json:"evidence_id,omitempty"`
	FailedQueryID string            `json:"failed_query_id,omitempty"`
	Status        string            `json:"status"`
	Summary       string            `json:"summary,omitempty"`
}

func (s AgentStep) String() string {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("step=%d agent=%s phase=%s name=%s", s.Index, s.Agent, s.Phase, s.Name)
	}
	return string(b)
}

type Recorder struct {
	mu           sync.Mutex
	steps        []AgentStep
	toolInputs   map[string][]string
	toolEvidence []ToolEvidence
}

func NewRecorder() *Recorder {
	return &Recorder{toolInputs: make(map[string][]string)}
}

func (r *Recorder) Record(step AgentStep) AgentStep {
	if r == nil {
		return step
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	step.Index = len(r.steps) + 1
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Now()
	}
	r.steps = append(r.steps, step)
	return step
}

func (r *Recorder) Steps() []AgentStep {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	steps := make([]AgentStep, len(r.steps))
	copy(steps, r.steps)
	return steps
}

func (r *Recorder) Strings() []string {
	steps := r.Steps()
	res := make([]string, 0, len(steps))
	for _, step := range steps {
		res = append(res, step.String())
	}
	return res
}

func (r *Recorder) ToolEvidence() []ToolEvidence {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	items := make([]ToolEvidence, len(r.toolEvidence))
	copy(items, r.toolEvidence)
	return items
}

func (r *Recorder) PushToolInput(name string, input string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolInputs == nil {
		r.toolInputs = make(map[string][]string)
	}
	r.toolInputs[name] = append(r.toolInputs[name], input)
}

func (r *Recorder) PopToolInput(name string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.toolInputs[name]
	if len(values) == 0 {
		return ""
	}
	value := values[0]
	r.toolInputs[name] = values[1:]
	return value
}

func (r *Recorder) RecordToolEvidence(item ToolEvidence) {
	if r == nil {
		return
	}
	item.ToolName = strings.TrimSpace(item.ToolName)
	item.EvidenceID = strings.TrimSpace(item.Envelope.EvidenceID)
	item.FailedQueryID = strings.TrimSpace(item.Envelope.FailedQueryID)
	item.Status = strings.TrimSpace(item.Envelope.Status)
	item.Summary = strings.TrimSpace(item.Envelope.Summary)
	if item.Summary == "" {
		item.Summary = strings.TrimSpace(item.Envelope.Message)
	}
	if item.Summary == "" {
		item.Summary = strings.TrimSpace(item.Envelope.Error)
	}
	if item.ToolName == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolEvidence = append(r.toolEvidence, item)
}

func Callback(agent string, recorder *Recorder) callbacks.Handler {
	builder := callbacks.NewHandlerBuilder()

	builder.OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		recordToolInput(info, input, recorder)
		recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "start",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Input:     compactJSON(input),
		})
		return ctx
	})

	builder.OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		recordToolOutput(info, output, recorder)
		recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "end",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Output:    compactJSON(output),
		})
		return ctx
	})

	builder.OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		recordToolError(info, err, recorder)
		return ctx
	})

	return builder.Build()
}

func StreamingCallback(agent string, recorder *Recorder, emit func(event string, step AgentStep)) callbacks.Handler {
	builder := callbacks.NewHandlerBuilder()

	builder.OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		if info == nil {
			return ctx
		}
		recordToolInput(info, input, recorder)
		step := recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "start",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Input:     compactJSON(input),
		})
		emitTraceStep(info, step, emit)
		return ctx
	})

	builder.OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		if info == nil {
			return ctx
		}
		recordToolOutput(info, output, recorder)
		step := recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "end",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Output:    compactJSON(output),
		})
		emitTraceStep(info, step, emit)
		return ctx
	})

	builder.OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil {
			return ctx
		}
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		recordToolError(info, err, recorder)
		step := recorder.Record(AgentStep{
			Agent:     agent,
			Phase:     "error",
			Component: string(info.Component),
			Type:      info.Type,
			Name:      info.Name,
			Error:     truncate(errText, 1000),
		})
		emitTraceStep(info, step, emit)
		return ctx
	})

	return builder.Build()
}

func recordToolInput(info *callbacks.RunInfo, input callbacks.CallbackInput, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	toolInput := tool.ConvCallbackInput(input)
	if toolInput == nil {
		return
	}
	recorder.PushToolInput(info.Name, toolInput.ArgumentsInJSON)
}

func recordToolOutput(info *callbacks.RunInfo, output callbacks.CallbackOutput, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	toolOutput := tool.ConvCallbackOutput(output)
	response := ""
	if toolOutput != nil {
		response = toolOutput.Response
	}
	parsed := evidence.Parse(response).Envelope
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName:   info.Name,
		InputJSON:  recorder.PopToolInput(info.Name),
		OutputJSON: response,
		Envelope:   parsed,
	})
}

func recordToolError(info *callbacks.RunInfo, err error, recorder *Recorder) {
	if info == nil || info.Component != components.ComponentOfTool {
		return
	}
	env := evidence.NewError(err, "tool callback error")
	recorder.RecordToolEvidence(ToolEvidence{
		ToolName:  info.Name,
		InputJSON: recorder.PopToolInput(info.Name),
		Envelope:  env,
	})
}

func emitTraceStep(info *callbacks.RunInfo, step AgentStep, emit func(event string, step AgentStep)) {
	if emit == nil || info == nil {
		return
	}
	if info.Component != components.ComponentOfTool {
		return
	}
	switch step.Phase {
	case "start":
		emit("tool_start", step)
	case "end":
		emit("tool_result", step)
	case "error":
		emit("tool_error", step)
	}
}

func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return truncate(string(b), 2000)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
