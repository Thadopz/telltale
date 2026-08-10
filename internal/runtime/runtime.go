package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Thadopz/telltale/internal/actions"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/internal/diagnosis"
	"github.com/Thadopz/telltale/internal/observability"
	"github.com/Thadopz/telltale/utility/common"
	"github.com/Thadopz/telltale/utility/storage"
	"github.com/gogf/gf/v2/frame/g"
)

// Runtime owns the shared dependencies used by CLI, HTTP, and watcher
// adapters. Adapters should depend on Diagnostic, not initialize models or
// storage themselves.
type Runtime struct {
	Diagnostic diagnosis.DiagnosticService
	Close      func(context.Context) error
}

// New initializes one local Telltale runtime from the active configuration.
// Configuration must be selected before calling this function.
func New(ctx context.Context) (*Runtime, error) {
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		return nil, err
	}
	if err := storage.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}
	if err := actions.Configure(ctx); err != nil {
		return nil, fmt.Errorf("configure actions: %w", err)
	}
	if err := configureFileDirectory(ctx); err != nil {
		return nil, err
	}

	shutdown, err := observability.Init(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize observability: %w", err)
	}
	return &Runtime{
		Diagnostic: diagnosis.NewService(),
		Close:      shutdown,
	}, nil
}

// NewRuntime is the descriptive alias used by adapters that want to make the
// bootstrap boundary explicit.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	return New(ctx)
}

func configureFileDirectory(ctx context.Context) error {
	common.FileDir = "data/docs"
	value, err := g.Cfg().Get(ctx, "file_dir")
	if err != nil || value == nil {
		return nil
	}
	configured := strings.TrimSpace(value.String())
	if configured != "" && !strings.Contains(configured, "YOUR_") {
		common.FileDir = configured
	}
	return nil
}

// CloseWithTimeout is convenient for command entrypoints that need to flush
// telemetry without keeping the process alive indefinitely.
func CloseWithTimeout(r *Runtime) error {
	if r == nil || r.Close == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.Close(ctx)
}
