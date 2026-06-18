package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

const defaultServerPort = 6872

func resolveServerPort(ctx context.Context) int {
	v, err := g.Cfg().Get(ctx, "server.address")
	if err != nil || v == nil {
		return defaultServerPort
	}
	return parseServerPort(v.String(), defaultServerPort)
}

func parseServerPort(address string, fallback int) int {
	address = strings.TrimSpace(address)
	if address == "" {
		return fallback
	}
	if strings.HasPrefix(address, ":") {
		address = strings.TrimPrefix(address, ":")
	}
	if idx := strings.LastIndex(address, ":"); idx >= 0 {
		address = address[idx+1:]
	}
	port, err := strconv.Atoi(strings.TrimSpace(address))
	if err != nil || port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
