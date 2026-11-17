package runstate

import (
	"context"
	"sync"
)

type stateKey struct{}

type questionKey struct{}

func WithQuestion(ctx context.Context, question string) context.Context {
	return context.WithValue(ctx, questionKey{}, question)
}

func Question(ctx context.Context) string {
	question, _ := ctx.Value(questionKey{}).(string)
	return question
}

type State struct {
	sync.Mutex
	completed  bool
	results    map[string]string
	queries    sync.Map
	diagnostic *Diagnostic
}

func LockQuery(ctx context.Context, key string) func() {
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return func() {}
	}
	value, _ := state.queries.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

const CompletionMessage = "外部任务工具已接受提交，调查已结束。该状态不代表根因已被独立验证。"

func WithState(ctx context.Context) context.Context {
	if _, ok := ctx.Value(stateKey{}).(*State); ok {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &State{results: make(map[string]string)})
}

func Complete(ctx context.Context) {
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		defer state.Unlock()
		state.completed = true
	}
}

func Completed(ctx context.Context) bool {
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return false
	}
	state.Lock()
	defer state.Unlock()
	return state.completed
}

func Cached(ctx context.Context, key string) (string, bool) {
	state, ok := ctx.Value(stateKey{}).(*State)
	if !ok {
		return "", false
	}
	state.Lock()
	defer state.Unlock()
	result, found := state.results[key]
	return result, found
}

func Cache(ctx context.Context, key, result string) {
	if state, ok := ctx.Value(stateKey{}).(*State); ok {
		state.Lock()
		defer state.Unlock()
		if len(state.results) < 32 {
			state.results[key] = result
		}
	}
}
