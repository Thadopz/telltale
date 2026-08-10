package main

import (
	"context"
	"time"

	"github.com/Thadopz/telltale/internal/appconfig"
	runtimepkg "github.com/Thadopz/telltale/internal/runtime"
	"github.com/Thadopz/telltale/internal/server"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"
)

// The historical root executable remains the HTTP-compatible entrypoint.
// New users should use cmd/telltale for explicit CLI subcommands.
func main() {
	ctx := gctx.New()
	appconfig.Configure(ctx)
	runtime, err := runtimepkg.NewRuntime(ctx)
	if err != nil {
		panic(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := runtime.Close(shutdownCtx); shutdownErr != nil {
			g.Log().Errorf(ctx, "shutdown OpenTelemetry: %v", shutdownErr)
		}
	}()
	if err := server.Run(ctx, runtime, server.Options{}); err != nil {
		panic(err)
	}
}
