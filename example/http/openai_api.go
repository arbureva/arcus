// 标准 OpenAI Chat Completions 兼容层。
//
// 让 Cherry Studio、LobeChat、NextChat 这类现成客户端零改动直连：客户端把
// API 地址填 http://localhost:8080/v1（Key 随便填），"模型"下拉框里出现的
// skills / cli / mcp / team 其实是本进程的四个服务——模型名就是路由。
//
// 与自有会话 API 的本质区别是状态归属：标准协议是无状态的，客户端每次把
// 完整 messages 发过来。这里的妙处在于零翻译成本——openai.Request 本身就是
// wire format，客户端的请求体直接 Unmarshal 进原生类型、原样种进 Transcript，
// 这正是"使用者清楚知道原生类型"这一设计的红利。
//
// 工具调用发生在服务端、对客户端不可见：客户端只拿到最终回答；流式时把
// 工具活动写进 reasoning_content，Cherry Studio 会渲染成"思考过程"。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/openai"
)

func registerOpenAI(mux *http.ServeMux, cfg *Config, services map[string]*service) {
	mux.HandleFunc("/v1/models", cors(func(w http.ResponseWriter, r *http.Request) {
		type model struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}
		out := struct {
			Object string  `json:"object"`
			Data   []model `json:"data"`
		}{Object: "list"}
		for name := range services {
			out.Data = append(out.Data, model{ID: name, Object: "model", Created: time.Now().Unix(), OwnedBy: "arcus"})
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("/v1/chat/completions", cors(completionsHandler(cfg, services)))
}

// cors 放开跨域，浏览器里跑的客户端（LobeChat 网页版等）需要；
// 桌面客户端（Cherry Studio）无所谓。鉴权按需自加——这里忽略 Bearer。
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// openaiErr 按 OpenAI 的错误格式返回，客户端才能正确弹提示。
func openaiErr(w http.ResponseWriter, code int, format string, a ...any) {
	writeJSON(w, code, map[string]any{"error": map[string]any{
		"message": fmt.Sprintf(format, a...),
		"type":    "invalid_request_error",
	}})
}

func completionsHandler(cfg *Config, services map[string]*service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			openaiErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}

		// 请求体直接落进原生类型——openai.Request 就是协议本身。
		var req openai.Request
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			openaiErr(w, http.StatusBadRequest, "bad json: %v", err)
			return
		}
		svc, ok := services[req.Model]
		if !ok {
			openaiErr(w, http.StatusNotFound,
				"unknown model %q, available: skills / cli / mcp / team", req.Model)
			return
		}

		requested := req.Model // 响应里按客户端要的名字回显
		stream := req.Stream
		req.Model = cfg.Model // 换成真实上游模型
		req.Stream = false
		req.Tools = nil // 工具由服务端管理，忽略客户端自带的
		req.Messages = append(
			[]openai.Message{openai.SystemMessage(svc.system)}, req.Messages...)

		// 无状态：客户端带来的完整历史直接种进一次性 Transcript。
		tr, err := agent.NewTranscript(adapter.OpenAI, &req)
		if err != nil {
			openaiErr(w, http.StatusInternalServerError, "transcript: %v", err)
			return
		}
		ag := svc.newAgent()

		if stream {
			streamCompletion(w, r, ag, tr, requested)
			return
		}

		out, err := ag.Run(r.Context(), tr)
		if err != nil {
			openaiErr(w, http.StatusBadGateway, "run: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   requested,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": out.Text()},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens":     out.Usage.InputTokens,
				"completion_tokens": out.Usage.OutputTokens,
				"total_tokens":      out.Usage.TotalTokens,
			},
		})
	}
}

// streamCompletion 把 agent 的归一化 chunk 流翻译成 OpenAI SSE 格式。
// 正文走 delta.content；模型思考与工具活动走 delta.reasoning_content ——
// 支持 DeepSeek 风格思考流的客户端（Cherry Studio 等）会把它渲染成
// 可折叠的"思考过程"，工具循环因此对用户可见而又不污染正文。
func streamCompletion(w http.ResponseWriter, r *http.Request, ag *agent.Agent, tr agent.Transcript, model string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		openaiErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, err := ag.RunStream(r.Context(), tr)
	if err != nil {
		openaiErr(w, http.StatusBadGateway, "stream: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	id, created := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()), time.Now().Unix()

	emit := func(delta map[string]any, finish any, usage *chat.Usage) {
		payload := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if usage != nil {
			payload["usage"] = map[string]int{
				"prompt_tokens":     usage.InputTokens,
				"completion_tokens": usage.OutputTokens,
				"total_tokens":      usage.TotalTokens,
			}
		}
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}

	emit(map[string]any{"role": "assistant"}, nil, nil)
	var total chat.Usage
	for c := range ch {
		switch c.Kind {
		case chat.ChunkText:
			delta, _ := chat.ChunkResult[string](&c)
			emit(map[string]any{"content": delta}, nil, nil)
		case chat.ChunkThinking:
			delta, _ := chat.ChunkResult[string](&c)
			emit(map[string]any{"reasoning_content": delta}, nil, nil)
		case chat.ChunkToolCall:
			frag, _ := chat.ChunkResult[*chat.ToolCallChunk](&c)
			if frag.Name != "" {
				emit(map[string]any{"reasoning_content": "\n🔧 " + frag.Name + " "}, nil, nil)
			}
			if frag.ArgsDelta != "" { // 参数增量也进思考流，工具循环全程可见
				emit(map[string]any{"reasoning_content": frag.ArgsDelta}, nil, nil)
			}
		case agent.ChunkToolResult:
			ret, _ := agent.AsToolReturn(&c)
			mark := "→"
			if ret.Result.IsError {
				mark = "✗"
			}
			emit(map[string]any{"reasoning_content": fmt.Sprintf(
				"\n%s %s\n", mark, truncate(ret.Result.Content, 200))}, nil, nil)
		case chat.ChunkUsage:
			u, _ := chat.ChunkResult[*chat.Usage](&c)
			total.InputTokens += u.InputTokens
			total.OutputTokens += u.OutputTokens
			total.TotalTokens += u.TotalTokens
		case chat.ChunkError:
			err, _ := chat.ChunkResult[error](&c)
			// 标准流没有错误信道，砍掉连接前把原因写进正文，客户端至少能看见。
			emit(map[string]any{"content": "\n[error] " + err.Error()}, "stop", nil)
			fmt.Fprint(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
	}
	emit(map[string]any{}, "stop", &total)
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func truncate(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
