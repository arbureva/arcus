package openai

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"
)

// The driver renders tools into the native request on every call. It must do
// that on a copy: a caller reusing one request across calls would otherwise
// accumulate duplicate tool definitions.
func TestNativeRequestDoesNotMutateCaller(t *testing.T) {
	caller := &sdk.Request{Model: "m", Tools: []sdk.Tool{{Type: "function", Function: sdk.Function{Name: "native"}}}}
	boxed := tool.NewSet(tool.Func("echo", "echoes", nil,
		func(context.Context, json.RawMessage) (*tool.Result, error) { return tool.Text("ok"), nil })).RequestTools()

	for i := 0; i < 2; i++ {
		nr, err := nativeRequest(caller)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyTools(nr, boxed); err != nil {
			t.Fatal(err)
		}
		if len(nr.Tools) != 2 {
			t.Fatalf("call %d: rendered tools = %d, want 2", i, len(nr.Tools))
		}
		nr.Stream = true
	}
	if len(caller.Tools) != 1 || caller.Tools[0].Function.Name != "native" {
		t.Errorf("caller's request was mutated: %+v", caller.Tools)
	}
	if caller.Stream {
		t.Error("caller's request had Stream set")
	}
}
