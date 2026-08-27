package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioEndpoint launches an MCP server as a child process and speaks
// newline-delimited JSON-RPC over its stdin/stdout (the MCP stdio transport).
// Build one with Stdio, optionally set Dir/Env, then hand it to Dial.
type StdioEndpoint struct {
	Command string
	Args    []string
	// Dir is the child's working directory ("" = inherit).
	Dir string
	// Env is the child's environment (nil = inherit the parent's).
	Env []string
	// Stderr receives the child's stderr (nil = the parent's stderr), which is
	// where MCP servers put their logs.
	Stderr io.Writer
}

// Stdio describes a subprocess MCP server:
//
//	c, err := mcp.Dial(ctx, mcp.Stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "/data"))
func Stdio(command string, args ...string) *StdioEndpoint {
	return &StdioEndpoint{Command: command, Args: args}
}

func (e *StdioEndpoint) dial(ctx context.Context) (transport, error) {
	cmd := exec.Command(e.Command, e.Args...)
	cmd.Dir = e.Dir
	cmd.Env = e.Env
	if e.Stderr != nil {
		cmd.Stderr = e.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", e.Command, err)
	}
	t := newPipeTransport(stdin, stdout, func() error {
		_ = cmd.Process.Kill()
		return cmd.Wait()
	})
	return t, nil
}

// pipeTransport multiplexes JSON-RPC over any line-oriented byte pipe. The
// stdio endpoint uses it with a subprocess; tests use it with in-memory pipes.
type pipeTransport struct {
	w       io.WriteCloser
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan *rpcResponse
	closed  bool
	readErr error

	shutdown func() error
	done     chan struct{}
	closing  atomic.Bool
}

func newPipeTransport(w io.WriteCloser, r io.Reader, shutdown func() error) *pipeTransport {
	t := &pipeTransport{
		w:        w,
		pending:  make(map[int64]chan *rpcResponse),
		shutdown: shutdown,
		done:     make(chan struct{}),
	}
	go t.readLoop(r)
	return t
}

func (t *pipeTransport) readLoop(r io.Reader) {
	defer close(t.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcResponse
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // not ours to crash over; skip malformed lines
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			// Server-initiated request. Answer ping; refuse the rest so the
			// server is not left hanging.
			t.answerServerRequest(&msg)
		case msg.Method != "":
			// Notification — nothing a tool client needs to act on.
		case len(msg.ID) > 0:
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err != nil {
				continue
			}
			t.mu.Lock()
			ch := t.pending[id]
			delete(t.pending, id)
			t.mu.Unlock()
			if ch != nil {
				ch <- &msg
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	t.failAll(fmt.Errorf("mcp: transport closed: %w", err))
}

func (t *pipeTransport) answerServerRequest(msg *rpcResponse) {
	type reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  interface{}     `json:"result,omitempty"`
		Error   *RPCError       `json:"error,omitempty"`
	}
	out := reply{JSONRPC: "2.0", ID: msg.ID}
	if msg.Method == "ping" {
		out.Result = struct{}{}
	} else {
		out.Error = &RPCError{Code: -32601, Message: "method not supported by arcus client"}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	_ = t.writeLine(b)
}

func (t *pipeTransport) failAll(err error) {
	t.mu.Lock()
	if t.readErr == nil {
		t.readErr = err
	}
	pending := t.pending
	t.pending = make(map[int64]chan *rpcResponse)
	t.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

func (t *pipeTransport) writeLine(b []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := t.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (t *pipeTransport) call(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	ch := make(chan *rpcResponse, 1)
	t.mu.Lock()
	if t.closed || t.readErr != nil {
		err := t.readErr
		t.mu.Unlock()
		if err == nil {
			err = errors.New("mcp: transport closed")
		}
		return nil, err
	}
	t.pending[req.ID] = ch
	t.mu.Unlock()

	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := t.writeLine(b); err != nil {
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
		return nil, fmt.Errorf("mcp: write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			t.mu.Lock()
			err := t.readErr
			t.mu.Unlock()
			if err == nil {
				err = errors.New("mcp: transport closed")
			}
			return nil, err
		}
		return resp, nil
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (t *pipeTransport) notify(_ context.Context, method string, params any) error {
	b, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	return t.writeLine(b)
}

func (t *pipeTransport) setProtocolVersion(string) {}

func (t *pipeTransport) close() error {
	if !t.closing.CompareAndSwap(false, true) {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	_ = t.w.Close()
	var err error
	if t.shutdown != nil {
		err = t.shutdown()
	}
	<-t.done
	return err
}
