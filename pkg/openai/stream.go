package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Stream performs a streaming chat completion. The returned Stream yields
// chunks via Recv until io.EOF (the "[DONE]" sentinel), and accumulates the
// full ChatCompletion (retrievable with Completion()). The caller must Close it.
//
// If you want usage in the stream, set req.StreamOptions.IncludeUsage = true.
func (c *Client) Stream(ctx context.Context, req *Request) (*Stream, error) {
	if req == nil {
		return nil, fmt.Errorf("openai: nil request")
	}
	req.Stream = true

	resp, err := c.do(ctx, req, true)
	if err != nil {
		return nil, err
	}
	return &Stream{
		body:   resp.Body,
		reader: bufio.NewReaderSize(resp.Body, 64*1024),
		acc:    &ChatCompletion{Object: "chat.completion"},
	}, nil
}

// StreamFunc runs a streaming request, invoking fn for every chunk, and returns
// the accumulated completion once the stream ends.
func (c *Client) StreamFunc(ctx context.Context, req *Request, fn func(*ChatCompletionChunk) error) (*ChatCompletion, error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return stream.Completion(), nil
		}
		if err != nil {
			return stream.Completion(), err
		}
		if fn != nil {
			if err := fn(chunk); err != nil {
				return stream.Completion(), err
			}
		}
	}
}

// Stream decodes the SSE chunk stream and accumulates the final completion.
// It is not safe for concurrent use.
type Stream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	acc    *ChatCompletion

	err    error
	closed bool
}

// Recv returns the next chunk, or io.EOF after the "[DONE]" sentinel.
func (s *Stream) Recv() (*ChatCompletionChunk, error) {
	if s.err != nil {
		return nil, s.err
	}

	data, err := s.readData()
	if err == io.EOF {
		// The body ended without the "[DONE]" sentinel. That is a truncated
		// stream unless the model already told us why it stopped — some
		// compatible gateways just close after the final chunk.
		err = s.endErr()
	}
	if err != nil {
		s.err = err
		return nil, err
	}
	if string(data) == "[DONE]" {
		s.err = io.EOF
		return nil, io.EOF
	}

	var chunk ChatCompletionChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		s.err = fmt.Errorf("openai: decode stream chunk: %w", err)
		return nil, s.err
	}
	s.accumulate(&chunk)
	return &chunk, nil
}

// readData reads SSE lines until a blank line and returns the concatenated
// data payload. Comment lines and non-data fields are ignored.
func (s *Stream) readData() ([]byte, error) {
	var data strings.Builder
	haveData := false

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if haveData {
					return []byte(data.String()), nil
				}
				return nil, io.EOF
			}
			return nil, fmt.Errorf("openai: read stream: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if haveData {
				return []byte(data.String()), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		if field == "data" {
			if haveData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			haveData = true
		}
	}
}

// accumulate folds a chunk into the in-progress completion.
func (s *Stream) accumulate(chunk *ChatCompletionChunk) {
	if s.acc.ID == "" {
		s.acc.ID = chunk.ID
	}
	if chunk.Model != "" {
		s.acc.Model = chunk.Model
	}
	if chunk.Created != 0 {
		s.acc.Created = chunk.Created
	}
	if chunk.SystemFingerprint != "" {
		s.acc.SystemFingerprint = chunk.SystemFingerprint
	}
	if chunk.Usage != nil {
		s.acc.Usage = chunk.Usage
	}

	for _, cc := range chunk.Choices {
		ch := s.ensureChoice(cc.Index)
		if cc.Delta.Role != "" {
			ch.Message.Role = cc.Delta.Role
		}
		ch.Message.Content += cc.Delta.Content
		ch.Message.ReasoningContent += cc.Delta.Thinking()
		ch.Message.Refusal += cc.Delta.Refusal
		if cc.FinishReason != nil {
			ch.FinishReason = *cc.FinishReason
		}
		for _, tc := range cc.Delta.ToolCalls {
			s.mergeToolCall(ch, tc)
		}
	}
}

func (s *Stream) ensureChoice(index int) *Choice {
	for len(s.acc.Choices) <= index {
		s.acc.Choices = append(s.acc.Choices, Choice{Index: len(s.acc.Choices)})
	}
	return &s.acc.Choices[index]
}

// mergeToolCall folds a streamed tool-call fragment into the choice's message.
func (s *Stream) mergeToolCall(ch *Choice, frag ToolCall) {
	idx := 0
	if frag.Index != nil {
		idx = *frag.Index
	}
	for len(ch.Message.ToolCalls) <= idx {
		ch.Message.ToolCalls = append(ch.Message.ToolCalls, ToolCall{})
	}
	dst := &ch.Message.ToolCalls[idx]
	if frag.ID != "" {
		dst.ID = frag.ID
	}
	if frag.Type != "" {
		dst.Type = frag.Type
	}
	if frag.Function.Name != "" {
		dst.Function.Name += frag.Function.Name
	}
	dst.Function.Arguments += frag.Function.Arguments
}

// endErr classifies a body that ended without "[DONE]": clean io.EOF if a
// finish reason arrived, ErrUnexpectedEOF otherwise. A caller that treats the
// latter as a normal end would hand a half-written answer to the user.
func (s *Stream) endErr() error {
	for i := range s.acc.Choices {
		if s.acc.Choices[i].FinishReason != "" {
			return io.EOF
		}
	}
	return fmt.Errorf("openai: stream ended mid-response: %w", io.ErrUnexpectedEOF)
}

// Completion returns the completion accumulated so far. After Recv returns
// io.EOF it is the complete response.
func (s *Stream) Completion() *ChatCompletion {
	return s.acc
}

// Err returns the terminal error, treating io.EOF as a clean end (nil).
func (s *Stream) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

// Close releases the underlying connection. Safe to call multiple times.
func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	drainAndClose(s.body)
	return nil
}
