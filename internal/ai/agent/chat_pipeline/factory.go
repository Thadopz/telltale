package chat_pipeline

import (
	"context"
	"github.com/Thadopz/telltale/internal/authz"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

var DefaultChatAgentFactory = NewChatAgentFactory()

type ChatAgentFactory struct {
	mu     sync.Mutex
	runner map[string]compose.Runnable[*UserMessage, *schema.Message]
}

func NewChatAgentFactory() *ChatAgentFactory {
	return &ChatAgentFactory{runner: make(map[string]compose.Runnable[*UserMessage, *schema.Message])}
}

func (f *ChatAgentFactory) Get(ctx context.Context) (compose.Runnable[*UserMessage, *schema.Message], error) {
	key := f.cacheKey(ctx)
	f.mu.Lock()
	if runner, ok := f.runner[key]; ok {
		f.mu.Unlock()
		return runner, nil
	}
	f.mu.Unlock()

	runner, err := BuildChatAgentWithOptions(ctx, ChatAgentOptions{})
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	if existing, ok := f.runner[key]; ok {
		f.mu.Unlock()
		return existing, nil
	}
	f.runner[key] = runner
	f.mu.Unlock()
	return runner, nil
}

func (f *ChatAgentFactory) cacheKey(ctx context.Context) string {
	principal := authz.PrincipalFromContext(ctx)
	roles := append([]string(nil), principal.Roles...)
	sort.Strings(roles)
	return strings.Join(roles, ",")
}
