package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"

	_ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

// getWeatherArgs is the tool's parameter struct; tool.Reflect turns it into the
// JSON Schema advertised to the model.
type getWeatherArgs struct {
	City  string `json:"city"            desc:"City name, e.g. Shanghai"`
	Units string `json:"units,omitempty" enum:"c,f" desc:"temperature unit"`
}

// buildTools declares the tool set once. It is provider-agnostic: the handler is
// the lowest-common-denominator shape func(ctx, json.RawMessage) (*tool.Result,
// error), and each driver renders set.RequestTools() into its own native tool
// list (OpenAI/DeepSeek function tools, Anthropic custom tools). The same Set is
// reused both to advertise the tools and to dispatch the model's tool-calls.
func buildTools() *tool.Set {
	return tool.NewSet(tool.Func("get_weather", "Get the current weather for a city",
		tool.Reflect(getWeatherArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var a getWeatherArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			// ... real lookup goes here ...
			return tool.Textf("It is 24°C and sunny in %s.", a.City), nil
		}),
	)
}

func main() {
	tools := buildTools()
	cli := chat.New()

	out, err := OpenAiChatTool(cli, tools)
	//out, err := DeepseekChatTool(cli, tools)
	//out, err := AnthropicChatTool(cli, tools)
	if err != nil {
		panic(err)
	}

	fmt.Printf("AI: %s\n", out.Text)
}
