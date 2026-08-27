package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arbureva/arcus/pkg/tool"
)

type weatherArgs struct {
	City  string   `json:"city"            desc:"City name"`
	Units string   `json:"units,omitempty" enum:"c,f"`
	Days  []string `json:"days,omitempty"`
	Note  *string  `json:"note,omitempty"`
}

func TestFuncInvokeAndDefinition(t *testing.T) {
	wx := tool.Func("get_weather", "Look up weather", tool.Reflect(weatherArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var a weatherArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return tool.Errf("bad args: %v", err), nil
			}
			return tool.Textf("sunny in %s", a.City), nil
		})

	d := wx.Definition()
	if d.Name != "get_weather" || d.Description != "Look up weather" {
		t.Fatalf("definition = %+v", d)
	}

	res, err := wx.Invoke(context.Background(), json.RawMessage(`{"city":"Paris"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Content != "sunny in Paris" {
		t.Fatalf("result = %+v", res)
	}

	// bad JSON -> error result, not a host error
	res, err = wx.Invoke(context.Background(), json.RawMessage(`{`))
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
}

func TestReflectSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(tool.Reflect(weatherArgs{}), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("type = %v", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	if props["city"].(map[string]any)["type"] != "string" {
		t.Errorf("city prop = %v", props["city"])
	}
	if props["city"].(map[string]any)["description"] != "City name" {
		t.Errorf("city desc missing: %v", props["city"])
	}
	enum := props["units"].(map[string]any)["enum"].([]any)
	if len(enum) != 2 || enum[0] != "c" || enum[1] != "f" {
		t.Errorf("units enum = %v", enum)
	}
	if props["days"].(map[string]any)["type"] != "array" {
		t.Errorf("days = %v", props["days"])
	}
	// only `city` is required: units/days have omitempty, note is a pointer
	req := schema["required"].([]any)
	if len(req) != 1 || req[0] != "city" {
		t.Errorf("required = %v", req)
	}
}

func TestReflectTypeMatchesValue(t *testing.T) {
	a := string(tool.Reflect(weatherArgs{}))
	b := string(tool.ReflectType[weatherArgs]())
	if a != b {
		t.Errorf("Reflect and ReflectType disagree:\n%s\n%s", a, b)
	}
}

func TestSet(t *testing.T) {
	a := tool.Func("a", "", nil, func(context.Context, json.RawMessage) (*tool.Result, error) {
		return tool.Text("A"), nil
	})
	b := tool.Func("b", "", nil, func(context.Context, json.RawMessage) (*tool.Result, error) {
		return tool.Text("B"), nil
	})
	set := tool.NewSet(a, b)

	if set.Len() != 2 {
		t.Fatalf("len = %d", set.Len())
	}
	if names := set.Names(); names[0] != "a" || names[1] != "b" {
		t.Errorf("order not preserved: %v", names)
	}
	if !set.Has("a") || set.Has("zzz") {
		t.Error("Has wrong")
	}

	res, err := set.Invoke(context.Background(), "b", nil)
	if err != nil || res.Content != "B" {
		t.Fatalf("invoke b = %+v %v", res, err)
	}
	if _, err := set.Invoke(context.Background(), "missing", nil); err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestSetDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate name")
		}
	}()
	noop := func(context.Context, json.RawMessage) (*tool.Result, error) { return tool.Text(""), nil }
	tool.NewSet(
		tool.Func("dup", "", nil, noop),
		tool.Func("dup", "", nil, noop),
	)
}

func TestDefinitionsOf(t *testing.T) {
	set := tool.NewSet(tool.Func("x", "desc", json.RawMessage(`{"type":"object"}`), func(context.Context, json.RawMessage) (*tool.Result, error) {
		return tool.Text(""), nil
	}))

	// Tool implements Definer
	defs, ok := tool.DefinitionsOf(set.RequestTools())
	if !ok || len(defs) != 1 || defs[0].Name != "x" {
		t.Fatalf("from tools: %v %+v", ok, defs)
	}

	// raw Definition values pass through too
	defs, ok = tool.DefinitionsOf([]interface{}{tool.Definition{Name: "y"}})
	if !ok || defs[0].Name != "y" {
		t.Fatalf("from definition: %v %+v", ok, defs)
	}

	// a foreign type is rejected
	if _, ok := tool.DefinitionsOf([]interface{}{42}); ok {
		t.Error("expected reject for non-tool element")
	}

	// nil/empty is ok
	if defs, ok := tool.DefinitionsOf(nil); !ok || defs != nil {
		t.Error("nil should be ok with nil result")
	}
}
