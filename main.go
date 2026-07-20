package main

import (
	"SREagent/internal/appconfig"
	"SREagent/internal/controller/chat"
	"SREagent/internal/observability"
	"SREagent/utility/client"
	"SREagent/utility/common"
	"SREagent/utility/middleware"
	"context"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gctx"
)

func main() {
	ctx := gctx.New()
	appconfig.Configure(ctx)
	shutdownObservability, err := observability.Init(ctx)
	if err != nil {
		panic(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdownObservability(shutdownCtx); shutdownErr != nil {
			g.Log().Errorf(ctx, "shutdown OpenTelemetry: %v", shutdownErr)
		}
	}()
	fileDir, err := g.Cfg().Get(ctx, "file_dir")
	if err != nil {
		panic(err)
	}
	common.FileDir = fileDir.String()
	if err = client.InitMySQL(ctx); err != nil {
		panic(err)
	}
	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.CORSMiddleware)
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(chat.NewV1())
	})
	s.SetPort(resolveServerPort(ctx))
	s.Run()
}
