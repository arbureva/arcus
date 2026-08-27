package chat

import (
	"context"
	"fmt"
	"sync"

	"github.com/arbureva/arcus/pkg/adapter"
)

// Client is the single entry point business code calls. It dispatches an
// adapter.Request to the configured provider and returns adapter types, hiding
// provider-specific detail. It is safe for concurrent use.
type Client struct {
	mu    sync.RWMutex
	conns map[adapter.Provider]Conn
}

// New returns an empty Client. Configure providers with Use before calling Chat
// or ChatStream.
func New() *Client {
	return &Client{conns: make(map[adapter.Provider]Conn)}
}

// Use opens the registered driver for p with cfg and stores the connection.
// cfg is typically the provider's native Config value (e.g. openai.Config) or
// json.RawMessage decoded from the application config file. Calling Use again
// for the same provider replaces the connection.
func (c *Client) Use(p adapter.Provider, cfg any) error {
	d, err := driverFor(p)
	if err != nil {
		return err
	}
	conn, err := d.Open(cfg)
	if err != nil {
		return fmt.Errorf("chat: open provider %q: %w", p, err)
	}
	c.mu.Lock()
	c.conns[p] = conn
	c.mu.Unlock()
	return nil
}

// Configured reports whether a provider has been set up with Use.
func (c *Client) Configured(p adapter.Provider) bool {
	c.mu.RLock()
	_, ok := c.conns[p]
	c.mu.RUnlock()
	return ok
}

func (c *Client) conn(p adapter.Provider) (Conn, error) {
	c.mu.RLock()
	conn, ok := c.conns[p]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("chat: provider %q not configured; call Use first", p)
	}
	return conn, nil
}

// Chat runs a non-streaming completion. req.Data must be the provider's native
// request value. The result is an adapter.MessageAdapter whose Data is a
// *Completion (use chat.Result to extract it).
func (c *Client) Chat(ctx context.Context, req adapter.Request) (*adapter.MessageAdapter, error) {
	conn, err := c.conn(req.Provider)
	if err != nil {
		return nil, err
	}
	return conn.Chat(ctx, req)
}

// ChatStream runs a streaming completion. req.Data must be the provider's
// native request value. It returns a channel of normalized chunks; the channel
// is closed when the stream ends. Transport/stream errors arrive as a final
// ChunkError chunk rather than a synchronous error — the synchronous error is
// reserved for setup problems (unconfigured provider, request type mismatch).
//
// The consumer should drain the channel or cancel ctx to release resources.
func (c *Client) ChatStream(ctx context.Context, req adapter.Request) (<-chan adapter.ChunkMessageAdapter, error) {
	conn, err := c.conn(req.Provider)
	if err != nil {
		return nil, err
	}

	ch := make(chan adapter.ChunkMessageAdapter)
	go func() {
		defer close(ch)
		emit := func(m adapter.ChunkMessageAdapter) bool {
			select {
			case ch <- m:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if err := conn.Stream(ctx, req, emit); err != nil {
			emit(adapter.ChunkMessageAdapter{Kind: ChunkError, Data: err})
		}
	}()
	return ch, nil
}
