// Package openai registers the OpenAI driver for Arcus's chat layer.
// Blank-import it to enable provider adapter.OpenAI:
//
//	import _ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/ecode"
	sdk "github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"
)

func init() {
	chat.Register(adapter.OpenAI, driver{})
}

type driver struct{}

func (driver) Open(cfg any) (chat.Conn, error) {
	c, err := toConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &conn{client: sdk.New(c)}, nil
}

func toConfig(cfg any) (sdk.Config, error) {
	switch v := cfg.(type) {
	case sdk.Config:
		return v, nil
	case *sdk.Config:
		return *v, nil
	case json.RawMessage:
		var c sdk.Config
		return c, json.Unmarshal(v, &c)
	case []byte:
		var c sdk.Config
		return c, json.Unmarshal(v, &c)
	default:
		return sdk.Config{}, ecode.TypeMismatch
	}
}

type conn struct{ client *sdk.Client }

func nativeRequest(req any) (*sdk.Request, error) {
	switch v := req.(type) {
	case *sdk.Request:
		return v, nil
	case sdk.Request:
		return &v, nil
	default:
		return nil, ecode.TypeMismatch
	}
}

// applyTools renders the provider-agnostic tools carried on adapter.Request.Tools
// into the native request's function-tool list (appended, so natively-set tools
// are preserved). A nil/empty list is a no-op.
func applyTools(nr *sdk.Request, raw []interface{}) error {
	defs, ok := tool.DefinitionsOf(raw)
	if !ok {
		return ecode.TypeMismatch
	}
	for _, d := range defs {
		nr.Tools = append(nr.Tools, sdk.Tool{
			Type: "function",
			Function: sdk.Function{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Schema,
			},
		})
	}
	return nil
}

func (c *conn) Chat(ctx context.Context, req adapter.Request) (*adapter.MessageAdapter, error) {
	nr, err := nativeRequest(req.Data)
	if err != nil {
		return nil, err
	}
	if err := applyTools(nr, req.Tools); err != nil {
		return nil, err
	}
	resp, err := c.client.Chat(ctx, nr)
	if err != nil {
		return nil, err
	}

	comp := &chat.Completion{
		Text:       resp.Text(),
		StopReason: resp.FinishReason(),
		Raw:        resp,
	}
	for _, tc := range resp.ToolCalls() {
		comp.ToolCalls = append(comp.ToolCalls, chat.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: json.RawMessage(tc.Function.Arguments),
		})
	}
	if resp.Usage != nil {
		comp.Usage = usageOf(resp.Usage)
	}
	return &adapter.MessageAdapter{
		Provider: adapter.OpenAI,
		Role:     adapter.RoleAssistant,
		Data:     comp,
	}, nil
}

func (c *conn) Stream(ctx context.Context, req adapter.Request, emit func(adapter.ChunkMessageAdapter) bool) error {
	nr, err := nativeRequest(req.Data)
	if err != nil {
		return err
	}
	if err := applyTools(nr, req.Tools); err != nil {
		return err
	}
	if nr.StreamOptions == nil {
		nr.StreamOptions = &sdk.StreamOptions{IncludeUsage: true}
	}

	stream, err := c.client.Stream(ctx, nr)
	if err != nil {
		return err
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		if len(chunk.Choices) == 0 {
			if chunk.Usage != nil && !emit(usageChunk(chunk.Usage)) {
				return ctx.Err()
			}
			continue
		}
		ch0 := chunk.Choices[0]
		if ch0.Delta.Content != "" {
			if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkText, Data: ch0.Delta.Content}) {
				return ctx.Err()
			}
		}
		for _, tc := range ch0.Delta.ToolCalls {
			if !emit(toolCallChunk(tc)) {
				return ctx.Err()
			}
		}
		if ch0.FinishReason != nil && *ch0.FinishReason != "" {
			if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkStop, Data: *ch0.FinishReason}) {
				return ctx.Err()
			}
		}
		if chunk.Usage != nil && !emit(usageChunk(chunk.Usage)) {
			return ctx.Err()
		}
	}
}

func toolCallChunk(tc sdk.ToolCall) adapter.ChunkMessageAdapter {
	idx := 0
	if tc.Index != nil {
		idx = *tc.Index
	}
	return adapter.ChunkMessageAdapter{Kind: chat.ChunkToolCall, Data: &chat.ToolCallChunk{
		Index:     idx,
		ID:        tc.ID,
		Name:      tc.Function.Name,
		ArgsDelta: tc.Function.Arguments,
	}}
}

func usageOf(u *sdk.Usage) *chat.Usage {
	return &chat.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
		Raw:          u,
	}
}

func usageChunk(u *sdk.Usage) adapter.ChunkMessageAdapter {
	return adapter.ChunkMessageAdapter{Kind: chat.ChunkUsage, Data: usageOf(u)}
}
