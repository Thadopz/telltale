package main

import (
	"github.com/Thadopz/telltale/internal/ai/agent/chat_pipeline"
	"github.com/Thadopz/telltale/internal/appconfig"
	"github.com/Thadopz/telltale/utility/mem"
	"github.com/Thadopz/telltale/utility/storage"
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()
	appconfig.Configure(ctx)
	if err := appconfig.ValidateRuntimeSecurity(ctx); err != nil {
		panic(err)
	}
	if err := storage.Initialize(ctx); err != nil {
		panic(err)
	}
	id := "111"
	userMessage := &chat_pipeline.UserMessage{
		ID:      id,
		Query:   "你好",
		History: mem.GetSimpleMemory(id).GetMessages(),
	}
	runner, err := chat_pipeline.BuildChatAgent(ctx)
	if err != nil {
		panic(err)
	}
	// 第一次对话
	out, err := runner.Invoke(ctx, userMessage)
	if err != nil {
		panic(err)
	}
	answer := out.Content
	fmt.Println("Q: 你好")
	fmt.Println("A:", answer)
	mem.GetSimpleMemory(id).SetMessages(schema.UserMessage("你好"))
	mem.GetSimpleMemory(id).SetMessages(schema.SystemMessage(out.Content))
	// 第二次对话
	userMessage = &chat_pipeline.UserMessage{
		ID:      id,
		Query:   "现在是几点",
		History: mem.GetSimpleMemory(id).GetMessages(),
	}
	out, err = runner.Invoke(ctx, userMessage)
	if err != nil {
		panic(err)
	}
	answer = out.Content
	fmt.Println("----------------")
	fmt.Println("Q: 现在是几点")
	fmt.Println("A:", answer)
}
