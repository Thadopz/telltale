package tools

import (
	"SREagent/internal/ai/evidence"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type testInvokableTool struct {
	name string
	run  func(context.Context) (string, error)
}

func (t *testInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t *testInvokableTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	return t.run(ctx)
}

func TestInvokeToolWithTimeoutReturnsCompletedOutput(t *testing.T) {
	inner := &testInvokableTool{
		name: "fast_tool",
		run: func(context.Context) (string, error) {
			return "done", nil
		},
	}

	output, err := invokeToolWithTimeout(context.Background(), inner, inner.name, "{}", time.Second)
	if err != nil {
		t.Fatalf("invoke tool: %v", err)
	}
	if output != "done" {
		t.Fatalf("expected completed output, got %q", output)
	}
}

func TestInvokeToolWithTimeoutReturnsStructuredTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	inner := &testInvokableTool{
		name: "blocked_tool",
		run: func(context.Context) (string, error) {
			<-block
			return "", nil
		},
	}

	output, err := invokeToolWithTimeout(context.Background(), inner, inner.name, "{}", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("expected structured tool result, got error %v", err)
	}

	env := evidence.Parse(output).Envelope
	if env.Status != evidence.StatusTimeout {
		t.Fatalf("expected timeout status, got %#v", env)
	}
	if env.ErrorCode != "TIMEOUT" {
		t.Fatalf("expected TIMEOUT error code, got %#v", env)
	}
}

func TestInvokeToolWithTimeoutKeepsOrdinaryError(t *testing.T) {
	expectedErr := errors.New("tool failed")
	inner := &testInvokableTool{
		name: "failed_tool",
		run: func(context.Context) (string, error) {
			return "", expectedErr
		},
	}

	_, err := invokeToolWithTimeout(context.Background(), inner, inner.name, "{}", time.Second)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected original error, got %v", err)
	}
}

func TestInvokeToolWithTimeoutReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	block := make(chan struct{})
	defer close(block)
	inner := &testInvokableTool{
		name: "blocked_tool",
		run: func(context.Context) (string, error) {
			<-block
			return "", nil
		},
	}

	cancel()
	_, err := invokeToolWithTimeout(ctx, inner, inner.name, "{}", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected parent cancellation, got %v", err)
	}
}

func TestLoadToolsWithTimeoutStopsWaitingForBlockedLoader(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	started := time.Now()
	_, err := loadToolsWithTimeout(context.Background(), 10*time.Millisecond, func(context.Context) ([]tool.BaseTool, error) {
		<-block
		return nil, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected registration timeout, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("registration timeout did not stop waiting promptly")
	}
}

func TestLoadSREGymEndpointsKeepsSuccessfulTools(t *testing.T) {
	endpoints := []sregymEndpoint{
		{Name: "failed", URL: "http://failed"},
		{Name: "working", URL: "http://working"},
	}
	workingTool := &testInvokableTool{
		name: "working_tool",
		run: func(context.Context) (string, error) {
			return "ok", nil
		},
	}

	tools, err := loadSREGymEndpoints(context.Background(), endpoints, func(_ context.Context, endpoint sregymEndpoint) ([]tool.BaseTool, error) {
		if endpoint.Name == "failed" {
			return nil, errors.New("endpoint unavailable")
		}
		return []tool.BaseTool{workingTool}, nil
	})

	if err == nil {
		t.Fatal("expected partial registration error")
	}
	if len(tools) != 1 {
		t.Fatalf("expected one successfully registered tool, got %d", len(tools))
	}
	info, infoErr := tools[0].Info(context.Background())
	if infoErr != nil || info.Name != "working_tool" {
		t.Fatalf("unexpected registered tool: info=%#v err=%v", info, infoErr)
	}
}
