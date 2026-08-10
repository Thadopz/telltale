package middleware

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

// CORSMiddleware 处理CORS跨域请求
func CORSMiddleware(r *ghttp.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if !originAllowed(origin, configuredAllowedOrigins(r.Context())) {
		r.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
		r.Response.WriteStatus(http.StatusForbidden, `{"message":"cross-origin request denied","data":null}`)
		r.ExitAll()
		return
	}

	if origin != "" {
		options := r.Response.DefaultCORSOptions()
		options.AllowOrigin = origin
		options.AllowCredentials = ""
		r.Response.Header().Set("Vary", "Origin")
		r.Response.CORS(options)
		if r.IsExited() {
			return
		}
	}
	r.Middleware.Next()
}

func configuredAllowedOrigins(ctx context.Context) []string {
	value, err := g.Cfg().Get(ctx, "server.allowed_origins")
	if err != nil || value == nil {
		return nil
	}
	return value.Strings()
}

func originAllowed(origin string, configured []string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}

	for _, allowed := range configured {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(allowed), "/"), origin) {
			return true
		}
	}

	host := strings.TrimSpace(parsed.Hostname())
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ResponseMiddleware(r *ghttp.Request) {
	r.Middleware.Next()
	r.Response.Header().Set("Content-Type", "application/json; charset=utf-8")

	var (
		msg string
		res = r.GetHandlerResponse()
		err = r.GetError()
	)
	if err != nil {
		msg = err.Error()
	} else {
		msg = "OK"
	}
	r.Response.WriteJson(Response{
		Message: msg,
		Data:    res,
	})
}

type Response struct {
	Message string      `json:"message" dc:"消息提示"`
	Data    interface{} `json:"data"    dc:"执行结果"`
}
