package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Thadopz/telltale/internal/controller/chat"
	runtimepkg "github.com/Thadopz/telltale/internal/runtime"
	"github.com/Thadopz/telltale/utility/middleware"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

const defaultAddress = "127.0.0.1:6872"

type Options struct {
	Host string
	Port int
}

// Run starts the compatibility HTTP adapter on top of an initialized
// runtime. The diagnostic service remains usable without this package.
func Run(ctx context.Context, runtime *runtimepkg.Runtime, options Options) error {
	if runtime == nil || runtime.Diagnostic == nil {
		return fmt.Errorf("runtime diagnostic service is unavailable")
	}

	address := configuredAddress(ctx)
	if strings.TrimSpace(options.Host) != "" || options.Port > 0 {
		host := options.Host
		if strings.TrimSpace(host) == "" {
			host = hostFromAddress(address)
		}
		port := options.Port
		if port <= 0 {
			port = portFromAddress(address)
		}
		address = netAddress(host, port)
	}
	if err := validateListenAddress(ctx, address); err != nil {
		return err
	}

	s := g.Server()
	s.Group("/api", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.CORSMiddleware)
		group.Middleware(middleware.ResponseMiddleware)
		group.Bind(chat.NewV1WithDiagnostic(runtime.Diagnostic))
	})
	s.SetAddr(address)
	s.Run()
	return nil
}

func validateListenAddress(ctx context.Context, address string) error {
	host := hostFromAddress(address)
	if isLoopbackHost(host) {
		return nil
	}
	value, err := g.Cfg().Get(ctx, "server.allow_remote")
	if err == nil && value != nil && value.Bool() {
		return nil
	}
	return fmt.Errorf("refusing non-loopback listen address %q; set server.allow_remote=true only behind an authenticated gateway", address)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func configuredAddress(ctx context.Context) string {
	value, err := g.Cfg().Get(ctx, "server.address")
	if err == nil && value != nil && strings.TrimSpace(value.String()) != "" {
		address := strings.TrimSpace(value.String())
		if strings.HasPrefix(address, ":") {
			return "127.0.0.1" + address
		}
		return address
	}
	return defaultAddress
}

func hostFromAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" || strings.HasPrefix(address, ":") {
		return "127.0.0.1"
	}
	if index := strings.LastIndex(address, ":"); index >= 0 {
		return strings.Trim(address[:index], "[]")
	}
	return address
}

func portFromAddress(address string) int {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, ":") {
		address = strings.TrimPrefix(address, ":")
	} else if index := strings.LastIndex(address, ":"); index >= 0 {
		address = address[index+1:]
	}
	port, err := strconv.Atoi(strings.TrimSpace(address))
	if err != nil || port < 1 || port > 65535 {
		return 6872
	}
	return port
}

func netAddress(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	if port < 1 || port > 65535 {
		port = 6872
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}
