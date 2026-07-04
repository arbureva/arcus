// Package mcp is a zero-dependency Model Context Protocol client that surfaces
// remote tools as first-class tool.Tool values. It is client-only: it can
// launch stdio servers and talk to Streamable HTTP servers, but it never
// exposes a service of its own.
//
// From the model's point of view an MCP tool is indistinguishable from a local
// one — it is still function calling; only the execution boundary moved:
//
//	c, err := mcp.Dial(ctx, mcp.Stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "/data"))
//	defer c.Close()
//
//	tools, err := c.Tools(ctx)          // []tool.Tool proxying tools/call
//	set := tool.NewSet(tools...)        // ...or box.Namespace("fs", ..., toolbox.Tools(tools...))
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/Arbureva/ice-adk/pkg/tool"
)

// transport is the wire strategy behind a Client. Implementations: pipe
// (stdio subprocess) and streamable HTTP.
type transport interface {
	call(ctx context.Context, req *rpcRequest) (*rpcResponse, error)
	notify(ctx context.Context, method string, params any) error
	setProtocolVersion(v string)
	close() error
}

// Endpoint describes where and how to reach an MCP server. Stdio and HTTP are
// the two provided constructors.
type Endpoint interface {
	dial(ctx context.Context) (transport, error)
}

// Option configures Dial.
type Option func(*Client)

// WithToolPrefix prefixes every proxied tool's advertised name, so tools from
// several servers can coexist in one set without clashing:
//
//	mcp.Dial(ctx, ep, mcp.WithToolPrefix("fs_"))   // read_file -> fs_read_file
//
// The prefix is stripped again before the call goes over the wire.
func WithToolPrefix(prefix string) Option {
	return func(c *Client) { c.prefix = prefix }
}

// WithClientInfo overrides the client identity sent during initialize.
func WithClientInfo(info Info) Option {
	return func(c *Client) { c.clientInfo = info }
}

// Client is a live, initialized connection to one MCP server. It is safe for
// concurrent use.
type Client struct {
	t          transport
	prefix     string
	clientInfo Info

	nextID       atomic.Int64
	serverInfo   Info
	version      string
	instructions string
}

// Dial connects to the endpoint and runs the MCP initialize handshake.
func Dial(ctx context.Context, ep Endpoint, opts ...Option) (*Client, error) {
	c := &Client{clientInfo: Info{Name: "ice-adk", Version: "1"}}
	for _, o := range opts {
		o(c)
	}
	t, err := ep.dial(ctx)
	if err != nil {
		return nil, err
	}
	c.t = t

	var init initializeResult
	err = c.call(ctx, "initialize", initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      c.clientInfo,
	}, &init)
	if err != nil {
		_ = t.close()
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}
	c.serverInfo = init.ServerInfo
	c.version = init.ProtocolVersion
	c.instructions = init.Instructions
	t.setProtocolVersion(init.ProtocolVersion)

	if err := t.notify(ctx, "notifications/initialized", struct{}{}); err != nil {
		_ = t.close()
		return nil, fmt.Errorf("mcp: initialized notification: %w", err)
	}
	return c, nil
}

// ServerInfo reports the server's identity from the handshake.
func (c *Client) ServerInfo() Info { return c.serverInfo }

// NegotiatedVersion reports the protocol version agreed at initialize.
func (c *Client) NegotiatedVersion() string { return c.version }

// Instructions returns the server-provided usage guidance from initialize, if
// any — a natural fit for a toolbox namespace's Instructions.
func (c *Client) Instructions() string { return c.instructions }

// Close tears the connection down (and, for stdio, the child process).
func (c *Client) Close() error { return c.t.close() }

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	req := &rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params}
	resp, err := c.t.call(ctx, req)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp: %s: %w", method, resp.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("mcp: %s: decode result: %w", method, err)
	}
	return nil
}

// ListTools fetches the server's tool catalogue (following pagination).
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var all []ToolInfo
	cursor := ""
	for {
		var page listToolsResult
		if err := c.call(ctx, "tools/list", listToolsParams{Cursor: cursor}, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// Call invokes one remote tool by its wire name (no prefix) and folds the MCP
// result into a tool.Result: text content blocks are concatenated, non-text
// blocks are annotated, and structuredContent is appended as JSON when there
// is no text at all.
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (*tool.Result, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var res callToolResult
	if err := c.call(ctx, "tools/call", callToolParams{Name: name, Arguments: args}, &res); err != nil {
		return nil, err
	}

	var out strings.Builder
	for _, block := range res.Content {
		switch block.Type {
		case "text":
			out.WriteString(block.Text)
		case "image", "audio":
			fmt.Fprintf(&out, "[%s content: %s, %d bytes base64]", block.Type, block.MimeType, len(block.Data))
		case "resource_link":
			fmt.Fprintf(&out, "[resource link: %s]", block.URI)
		case "resource":
			fmt.Fprintf(&out, "[embedded resource: %s]", compactJSON(block.Resource))
		default:
			fmt.Fprintf(&out, "[%s content]", block.Type)
		}
	}
	if out.Len() == 0 && len(res.StructuredContent) > 0 {
		out.WriteString(compactJSON(res.StructuredContent))
	}
	r := &tool.Result{Content: out.String(), IsError: res.IsError}
	if len(res.StructuredContent) > 0 {
		r.Meta = res.StructuredContent
	}
	return r, nil
}

// Tools lists the server's tools and wraps each as a tool.Tool whose Invoke
// proxies tools/call over this connection. The tools stay bound to the Client;
// close it and they stop working.
func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	infos, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(infos))
	for _, info := range infos {
		out = append(out, &remoteTool{client: c, info: info})
	}
	return out, nil
}

// ToolSet is Tools folded into a ready-to-use *tool.Set.
func (c *Client) ToolSet(ctx context.Context) (*tool.Set, error) {
	tools, err := c.Tools(ctx)
	if err != nil {
		return nil, err
	}
	return tool.NewSet(tools...), nil
}

type remoteTool struct {
	client *Client
	info   ToolInfo
}

func (t *remoteTool) Definition() tool.Definition {
	desc := t.info.Description
	if desc == "" {
		desc = t.info.Title
	}
	return tool.Definition{
		Name:        t.client.prefix + t.info.Name,
		Description: desc,
		Schema:      t.info.InputSchema,
	}
}

func (t *remoteTool) Invoke(ctx context.Context, args json.RawMessage) (*tool.Result, error) {
	return t.client.Call(ctx, t.info.Name, args)
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
