package anthropic

import (
	"encoding/json"
	"testing"
)

// An echoed thinking block with empty text must still carry "thinking" and
// "signature"; DeepSeek's Anthropic endpoint 422s on a missing field.
func TestContentBlockRequiredFields(t *testing.T) {
	cases := map[string]struct {
		in   ContentBlock
		want string
	}{
		"empty thinking": {ContentBlock{Type: BlockThinking}, `{"type":"thinking","thinking":"","signature":""}`},
		"thinking":       {ContentBlock{Type: BlockThinking, Thinking: "hm", Signature: "sig"}, `{"type":"thinking","thinking":"hm","signature":"sig"}`},
		"empty text":     {TextBlock(""), `{"type":"text","text":""}`},
		"redacted":       {ContentBlock{Type: BlockRedactedThinking, Data: "x"}, `{"type":"redacted_thinking","data":"x"}`},
		"tool_use":       {ToolUseBlock("id1", "f", json.RawMessage(`{}`)), `{"type":"tool_use","id":"id1","name":"f","input":{}}`},
	}
	for name, c := range cases {
		got, err := json.Marshal(c.in)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: got %s, %v; want %s", name, got, err, c.want)
		}
	}

	// Round trip through Message keeps the block intact.
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":""},{"type":"text","text":"hi"}]}`), &m); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(m.Content)
	if want := `[{"type":"thinking","thinking":"","signature":""},{"type":"text","text":"hi"}]`; string(got) != want {
		t.Errorf("round trip: got %s want %s", got, want)
	}
}
