package chat

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/arbureva/arcus/pkg/adapter"
)

// Conn is a live, configured connection to one provider. Drivers return it from
// Open; the chat Client holds one Conn per configured provider.
//
// The driver reads req.Data for the provider-native request value (e.g.
// *openai.Request) — type-asserting it and returning ecode.TypeMismatch on a
// mismatch — and req.Tools for any provider-agnostic tool.Tool/tool.Definition
// to render into the native request before sending.
type Conn interface {
	// Chat performs a non-streaming completion and returns the assistant reply
	// as an adapter.MessageAdapter whose Data is a *Completion.
	Chat(ctx context.Context, req adapter.Request) (*adapter.MessageAdapter, error)

	// Stream performs a streaming completion, calling emit for each normalized
	// chunk. emit returns false when the consumer has gone away (context
	// cancelled); the driver must then stop and return. Stream blocks until the
	// provider stream ends and returns the terminal error (nil on clean end).
	// It must NOT emit a ChunkError itself — the Client does that.
	Stream(ctx context.Context, req adapter.Request, emit func(adapter.ChunkMessageAdapter) bool) error
}

// Driver is the registration surface each provider bridge implements. Open
// builds a Conn from a config blob (the provider's native Config value, or
// json.RawMessage / []byte decoded from the application config file).
type Driver interface {
	Open(cfg any) (Conn, error)
}

var (
	driversMu sync.RWMutex
	drivers   = make(map[adapter.Provider]Driver)
)

// Register makes a driver available for the given provider. It is intended to
// be called from a driver package's init, so applications select providers
// with blank imports:
//
//	import _ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
//
// Register panics on a nil driver or a duplicate registration.
func Register(p adapter.Provider, d Driver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	if d == nil {
		panic("chat: Register driver is nil")
	}
	if _, dup := drivers[p]; dup {
		panic("chat: Register called twice for provider " + string(p))
	}
	drivers[p] = d
}

// Providers lists the providers that currently have a registered driver.
func Providers() []adapter.Provider {
	driversMu.RLock()
	defer driversMu.RUnlock()
	out := make([]adapter.Provider, 0, len(drivers))
	for p := range drivers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func driverFor(p adapter.Provider) (Driver, error) {
	driversMu.RLock()
	d, ok := drivers[p]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("chat: no driver registered for provider %q (missing blank import?)", p)
	}
	return d, nil
}
