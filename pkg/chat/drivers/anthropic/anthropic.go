// Package anthropic registers the Anthropic driver for Arcus's chat layer.
// Blank-import it to enable provider adapter.Anthropic:
//
//	import _ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/arbureva/arcus/pkg/adapter"
	sdk "github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/ecode"
	"github.com/arbureva/arcus/pkg/tool"
)

func init() {
	chat.Register(adapter.Anthropic, driver{})
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

// applyTools renders adapter.Request.Tools into Anthropic's native custom-tool
// list (appended; natively-set tools are preserved). A nil/empty list is a
// no-op.
func applyTools(nr *sdk.Request, raw []interface{}) error {
	defs, ok := tool.DefinitionsOf(raw)
	if !ok {
		return ecode.TypeMismatch
	}
	for _, d := range defs {
		schema := d.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		nr.Tools = append(nr.Tools, sdk.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: schema,
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
		Reasoning:  resp.Thinking(),
		StopReason: resp.StopReason,
		Raw:        resp,
	}
	for _, blk := range resp.ToolUses() {
		comp.ToolCalls = append(comp.ToolCalls, chat.ToolCall{
			ID:   blk.ID,
			Name: blk.Name,
			Args: blk.Input,
		})
	}
	if resp.Usage != nil {
		comp.Usage = usageOf(resp.Usage)
	}
	return &adapter.MessageAdapter{
		Provider: adapter.Anthropic,
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

	stream, err := c.client.Stream(ctx, nr)
	if err != nil {
		return err
	}
	defer stream.Close()

	var inputTokens int // captured at message_start, folded into the usage chunk

	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		switch ev.Type {
		case sdk.EventMessageStart:
			if ev.Message != nil && ev.Message.Usage != nil {
				inputTokens = ev.Message.Usage.InputTokens
			}

		case sdk.EventContentBlockStart:
			if ev.ContentBlock != nil && ev.ContentBlock.Type == sdk.BlockToolUse {
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkToolCall, Data: &chat.ToolCallChunk{
					Index: ev.Index,
					ID:    ev.ContentBlock.ID,
					Name:  ev.ContentBlock.Name,
				}}) {
					return ctx.Err()
				}
			}

		case sdk.EventContentBlockDelta:
			if t, ok := ev.TextDelta(); ok {
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkText, Data: t}) {
					return ctx.Err()
				}
			}
			if t, ok := ev.ThinkingDelta(); ok {
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkThinking, Data: t}) {
					return ctx.Err()
				}
			}
			if pj, ok := ev.InputJSONDelta(); ok {
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkToolCall, Data: &chat.ToolCallChunk{
					Index:     ev.Index,
					ArgsDelta: pj,
				}}) {
					return ctx.Err()
				}
			}

		case sdk.EventMessageDelta:
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkStop, Data: ev.Delta.StopReason}) {
					return ctx.Err()
				}
			}
			if ev.Usage != nil {
				u := &chat.Usage{
					InputTokens:  inputTokens,
					OutputTokens: ev.Usage.OutputTokens,
					TotalTokens:  inputTokens + ev.Usage.OutputTokens,
					Raw:          ev.Usage,
				}
				if !emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkUsage, Data: u}) {
					return ctx.Err()
				}
			}
		}
	}
}

func usageOf(u *sdk.Usage) *chat.Usage {
	return &chat.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.InputTokens + u.OutputTokens,
		Raw:          u,
	}
}
