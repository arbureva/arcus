package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Stream performs a streaming Messages request. The returned MessageStream
// yields decoded events via Recv until io.EOF, and accumulates the full
// assistant Message (retrievable with Message()). The caller must Close it.
func (c *Client) Stream(ctx context.Context, req *Request) (*MessageStream, error) {
	if req == nil {
		return nil, fmt.Errorf("anthropic: nil request")
	}
	req.Stream = true

	resp, err := c.do(ctx, req, true)
	if err != nil {
		return nil, err
	}
	return &MessageStream{
		body:    resp.Body,
		reader:  bufio.NewReaderSize(resp.Body, 64*1024),
		acc:     &Message{},
		toolBuf: map[int]*strings.Builder{},
	}, nil
}

// StreamFunc runs a streaming request, invoking fn for every event, and returns
// the accumulated assistant message once the stream ends.
func (c *Client) StreamFunc(ctx context.Context, req *Request, fn func(StreamEvent) error) (*Message, error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return stream.Message(), nil
		}
		if err != nil {
			return stream.Message(), err
		}
		if fn != nil {
			if err := fn(ev); err != nil {
				return stream.Message(), err
			}
		}
	}
}

// MessageStream decodes the SSE event stream and accumulates the final message.
// It is not safe for concurrent use.
type MessageStream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	acc    *Message

	// toolBuf accumulates input_json_delta fragments per content block index,
	// reassembled into ContentBlock.Input on content_block_stop.
	toolBuf map[int]*strings.Builder

	err    error
	closed bool
}

// Recv returns the next decoded event, or io.EOF after message_stop.
func (s *MessageStream) Recv() (StreamEvent, error) {
	if s.err != nil {
		return StreamEvent{}, s.err
	}

	evtName, data, err := s.readEvent()
	if err == io.EOF {
		// No message_stop: the body was cut short mid-response. Reporting a
		// clean end here would pass a half-written answer off as complete.
		err = fmt.Errorf("anthropic: stream ended mid-response: %w", io.ErrUnexpectedEOF)
	}
	if err != nil {
		s.err = err
		return StreamEvent{}, err
	}

	var ev StreamEvent
	if len(data) > 0 {
		if err := json.Unmarshal(data, &ev); err != nil {
			s.err = fmt.Errorf("anthropic: decode stream event: %w", err)
			return StreamEvent{}, s.err
		}
	}
	if ev.Type == "" {
		ev.Type = evtName
	}

	if ev.Type == EventError && ev.Error != nil {
		s.err = ev.Error
		return ev, s.err
	}

	s.accumulate(ev)

	if ev.Type == EventMessageStop {
		s.err = io.EOF
		return ev, nil
	}
	return ev, nil
}

// readEvent reads one SSE event, returning its event name and data payload.
func (s *MessageStream) readEvent() (string, []byte, error) {
	var event string
	var data strings.Builder
	haveData := false

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if haveData || event != "" {
					return event, []byte(data.String()), nil
				}
				return "", nil, io.EOF
			}
			return "", nil, fmt.Errorf("anthropic: read stream: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if haveData || event != "" {
				return event, []byte(data.String()), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if haveData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			haveData = true
		}
	}
}

// accumulate folds an event into the in-progress message.
func (s *MessageStream) accumulate(ev StreamEvent) {
	switch ev.Type {
	case EventMessageStart:
		if ev.Message != nil {
			*s.acc = *ev.Message
			if s.acc.Content == nil {
				s.acc.Content = []ContentBlock{}
			}
		}

	case EventContentBlockStart:
		blk := ContentBlock{}
		if ev.ContentBlock != nil {
			blk = *ev.ContentBlock
		}
		s.setBlock(ev.Index, blk)
		if blk.Type == BlockToolUse {
			s.toolBuf[ev.Index] = &strings.Builder{}
		}

	case EventContentBlockDelta:
		if ev.Delta == nil {
			return
		}
		blk := s.blockAt(ev.Index)
		switch ev.Delta.Type {
		case DeltaText:
			blk.Text += ev.Delta.Text
		case DeltaThinking:
			blk.Thinking += ev.Delta.Thinking
		case DeltaSignature:
			blk.Signature += ev.Delta.Signature
		case DeltaInputJSON:
			if s.toolBuf[ev.Index] == nil {
				s.toolBuf[ev.Index] = &strings.Builder{}
			}
			s.toolBuf[ev.Index].WriteString(ev.Delta.PartialJSON)
		}

	case EventContentBlockStop:
		if buf := s.toolBuf[ev.Index]; buf != nil {
			raw := buf.String()
			if raw == "" {
				raw = "{}"
			}
			s.blockAt(ev.Index).Input = json.RawMessage(raw)
			delete(s.toolBuf, ev.Index)
		}

	case EventMessageDelta:
		if ev.Delta != nil {
			if ev.Delta.StopReason != "" {
				s.acc.StopReason = ev.Delta.StopReason
			}
			if ev.Delta.StopSequence != "" {
				s.acc.StopSequence = ev.Delta.StopSequence
			}
		}
		if ev.Usage != nil {
			if s.acc.Usage == nil {
				s.acc.Usage = &Usage{}
			}
			// message_delta carries cumulative output tokens.
			s.acc.Usage.OutputTokens = ev.Usage.OutputTokens
			if ev.Usage.InputTokens != 0 {
				s.acc.Usage.InputTokens = ev.Usage.InputTokens
			}
		}
	}
}

func (s *MessageStream) setBlock(index int, blk ContentBlock) {
	for len(s.acc.Content) <= index {
		s.acc.Content = append(s.acc.Content, ContentBlock{})
	}
	s.acc.Content[index] = blk
}

func (s *MessageStream) blockAt(index int) *ContentBlock {
	for len(s.acc.Content) <= index {
		s.acc.Content = append(s.acc.Content, ContentBlock{})
	}
	return &s.acc.Content[index]
}

// Message returns the message accumulated so far. After Recv returns io.EOF it
// is the complete assistant message.
func (s *MessageStream) Message() *Message {
	return s.acc
}

// Err returns the terminal error, treating io.EOF as a clean end (nil).
func (s *MessageStream) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

// Close releases the underlying connection. Safe to call multiple times.
func (s *MessageStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	drainAndClose(s.body)
	return nil
}
