package anthropic

import "encoding/json"

// Content block type discriminators.
const (
	BlockText             = "text"
	BlockImage            = "image"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
)

// ContentBlock is a single block within a message's content array. A flat
// struct (rather than an interface) is used so blocks round-trip through
// json.Marshal/Unmarshal without custom dispatch; inspect Type to interpret it.
type ContentBlock struct {
	Type string `json:"type"`

	// text / thinking text
	Text string `json:"text,omitempty"`

	// image
	Source *ImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// redacted_thinking
	Data string `json:"data,omitempty"`
}

// MarshalJSON keeps the fields a block type requires even when they are empty.
// The flat struct needs omitempty for everything else, but an echoed thinking
// block with empty text would lose "thinking" entirely, and Anthropic-compatible
// servers that deserialize strictly (DeepSeek) reject the whole request with 422
// "missing field `thinking`".
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	type plain ContentBlock
	switch b.Type {
	case BlockThinking:
		return json.Marshal(struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}{b.Type, b.Thinking, b.Signature})
	case BlockText:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})
	case BlockRedactedThinking:
		return json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{b.Type, b.Data})
	}
	return json.Marshal(plain(b))
}

// ImageSource describes an image block payload: base64 bytes or a URL.
type ImageSource struct {
	Type      string `json:"type"` // "base64" or "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// TextBlock builds a text content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: BlockText, Text: text}
}

// ImageBase64Block builds an image block from base64-encoded data.
func ImageBase64Block(mediaType, base64Data string) ContentBlock {
	return ContentBlock{
		Type:   BlockImage,
		Source: &ImageSource{Type: "base64", MediaType: mediaType, Data: base64Data},
	}
}

// ImageURLBlock builds an image block referencing a URL.
func ImageURLBlock(url string) ContentBlock {
	return ContentBlock{
		Type:   BlockImage,
		Source: &ImageSource{Type: "url", URL: url},
	}
}

// ToolUseBlock builds a tool_use block. input must be valid JSON.
func ToolUseBlock(id, name string, input json.RawMessage) ContentBlock {
	return ContentBlock{Type: BlockToolUse, ID: id, Name: name, Input: input}
}

// ToolResultText builds a tool_result block whose content is plain text.
func ToolResultText(toolUseID, text string, isError bool) ContentBlock {
	raw, _ := json.Marshal(text)
	return ContentBlock{Type: BlockToolResult, ToolUseID: toolUseID, Content: raw, IsError: isError}
}

// ToolResultBlocks builds a tool_result block whose content is an array of
// content blocks (e.g. text + image).
func ToolResultBlocks(toolUseID string, blocks []ContentBlock, isError bool) (ContentBlock, error) {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{Type: BlockToolResult, ToolUseID: toolUseID, Content: raw, IsError: isError}, nil
}
