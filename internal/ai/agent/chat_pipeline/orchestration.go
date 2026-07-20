package chat_pipeline

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func BuildChatAgent(ctx context.Context) (r compose.Runnable[*UserMessage, *schema.Message], err error) {
	return DefaultChatAgentFactory.Get(ctx)
}

func BuildChatAgentWithOptions(ctx context.Context, opts ChatAgentOptions) (r compose.Runnable[*UserMessage, *schema.Message], err error) {
	const (
		PrepareContext   = "PrepareContext"
		ContextDocuments = "ContextDocuments"
		ChatTemplate     = "ChatTemplate"
		ADKAgent         = "ADKAgent"
		InputToChat      = "InputToChat"
	)

	g := compose.NewGraph[*UserMessage, *schema.Message]()
	if err = g.AddLambdaNode(PrepareContext, compose.InvokableLambda(newPrepareContextLambda), compose.WithNodeName("PrepareContext")); err != nil {
		return nil, fmt.Errorf("add prepare context node: %w", err)
	}
	if err = g.AddLambdaNode(ContextDocuments, compose.InvokableLambdaWithOption(newContextDocumentsLambdaWithOptions(opts)), compose.WithNodeName("ContextDocuments")); err != nil {
		return nil, fmt.Errorf("add context documents node: %w", err)
	}

	chatTemplate, err := newChatTemplate(ctx)
	if err != nil {
		return nil, err
	}
	if err = g.AddChatTemplateNode(ChatTemplate, chatTemplate); err != nil {
		return nil, fmt.Errorf("add chat template node: %w", err)
	}

	adkAgent, err := newADKAgentLambdaWithOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err = g.AddLambdaNode(ADKAgent, adkAgent, compose.WithNodeName("ADKChatModelAgent")); err != nil {
		return nil, fmt.Errorf("add ADK agent node: %w", err)
	}
	if err = g.AddLambdaNode(InputToChat, compose.InvokableLambdaWithOption(newInputToChatLambda), compose.WithNodeName("UserMessageToChat")); err != nil {
		return nil, fmt.Errorf("add chat input node: %w", err)
	}

	edges := [][2]string{
		{compose.START, PrepareContext},
		{PrepareContext, ContextDocuments},
		{PrepareContext, InputToChat},
		{ContextDocuments, ChatTemplate},
		{InputToChat, ChatTemplate},
		{ChatTemplate, ADKAgent},
		{ADKAgent, compose.END},
	}
	for _, edge := range edges {
		if err = g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, fmt.Errorf("add graph edge %s -> %s: %w", edge[0], edge[1], err)
		}
	}

	r, err = g.Compile(ctx, compose.WithGraphName("ChatAgent"), compose.WithNodeTriggerMode(compose.AllPredecessor))
	if err != nil {
		return nil, err
	}
	return r, nil
}
