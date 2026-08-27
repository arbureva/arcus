// Command http 演示如何把 arcus 接进你自己的 HTTP 服务。
//
// 对外暴露两层 API，对应两种接入姿势：
//
//	① 标准 OpenAI 兼容层（openai_api.go）—— 给现成客户端用。
//	   Cherry Studio、LobeChat、NextChat…… 任何支持"自定义 OpenAI 提供商"
//	   的客户端填上 http://localhost:8080/v1 就能直连。无状态：历史由
//	   客户端携带，"模型名"用来选服务（skills / cli / mcp / team）。
//
//	   GET  /v1/models             客户端拉模型列表
//	   POST /v1/chat/completions   标准协议，支持 stream
//
//	② 自有会话 API（本文件）—— 给你自己的前端用。服务端管历史，
//	   请求只带增量消息，会话状态（Transcript、工具箱披露进度）都在服务端。
//
//	   POST /chat/{skills|cli|mcp|team}
//	   {"conversation_id":"可选","message":"...","stream":false}
//
// 两层共用同一组"服务"定义。核心只有一条规则，理解了它就理解了全部：
//
//	进程级（启动装配一次，所有请求共享）：
//	    chat.Client   —— 无状态的厂商连接
//	    mcp.Client    —— 一条到 MCP server 的长连接，工具进程内共享
//	    []*skill.Skill、tool.Tool —— 纯数据 + 纯函数
//	    toolbox.Box   —— 作为"模板"注册好所有分组
//	    子 Agent      —— 无会话状态，构造零成本
//
//	会话级（每个对话一份）：
//	    agent.Transcript —— 消息历史（明确不支持并发）
//	    box.Clone()      —— 渐进式披露的激活状态
//
// 四个服务各自演示一种工具形态——你的服务多半只需要其中一种，
// 把对应的 xxxService 函数抄走即可：
//
//	skills  只挂 Skills（toolbox 折叠，模型按需打开）
//	cli     只挂命令行工具（argv 白名单，无 shell 注入面）
//	mcp     只挂 MCP 工具（config.json 里配置服务器）
//	team    多 Agent 协同（搜索专员 + 资料专员，都只是工具）
//
// 试一试：
//
//	go run ./example/http -config example/http/config.json
//
//	# 标准协议（Cherry Studio 里把 API 地址填 http://localhost:8080/v1，
//	# API Key 随便填，模型选 skills / cli / mcp / team）：
//	curl -s localhost:8080/v1/chat/completions -d '{
//	  "model": "cli",
//	  "messages": [{"role":"user","content":"现在几点？磁盘还够吗"}]
//	}'
//
//	# 自有会话 API：
//	curl -s localhost:8080/chat/skills -d '{"message":"帮我把这句翻成英文：冰莲响应，使命必达"}'
//	curl -sN localhost:8080/chat/team -d '{"message":"查一下 Go 1.25 新特性，按周报格式整理","stream":true}'
//
// 换厂商 = 改两处：cli.Use 的驱动 + seed 里 NewTranscript 的 provider 与原生
// Request 类型。handler、session、工具全部不动。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/cli"
	"github.com/arbureva/arcus/pkg/mcp"
	"github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/skill"
	"github.com/arbureva/arcus/pkg/tool"
	"github.com/arbureva/arcus/pkg/toolbox"

	_ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

// ═════════════════════════════ 配置 ═════════════════════════════
// 服务通过配置文件启动，不读环境变量。

type Config struct {
	Addr      string      `json:"addr"`
	APIKey    string      `json:"api_key"`
	BaseURL   string      `json:"base_url"`
	Model     string      `json:"model"`
	SkillsDir string      `json:"skills_dir"`
	MCP       []MCPServer `json:"mcp"`
}

// MCPServer 二选一：Command+Args 走 stdio，URL 走 Streamable HTTP。
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Addr: ":8080"}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// ═════════════════════════════ 服务 ═════════════════════════════
//
// service 是"一套系统提示词 + 一套工具"的组合，与暴露方式无关：
// 同一个 service 既挂在标准 OpenAI 兼容层下，也挂在自有会话 API 下。
//
// newAgent 每个会话调用一次。Agent 构造零成本，所以需要每会话独立工具
// 状态（如 Box 披露进度）时，直接在这里现造，不要犹豫；工具本身无状态
// 的服务（cli、mcp）返回共享实例即可。

type service struct {
	name     string
	system   string
	newAgent func() *agent.Agent
}

// ───────────────────────── 服务 1：只挂 Skills ─────────────────────────
//
// skills 目录进程级加载一次；Box 模板注册好全部 skill。每个会话 Clone 一份
// Box —— 披露状态（模型打开了哪些 skill）是会话自己的事。

func skillsService(cc *chat.Client, cfg *Config) *service {
	skills, err := skill.LoadDir(cfg.SkillsDir)
	if err != nil {
		log.Fatalf("load skills: %v", err)
	}
	tmpl := toolbox.New()
	tmpl.AddSkills(skills)

	return &service{
		name:   "skills",
		system: "你是办公助手。工具箱里有若干技能，需要时打开对应技能并严格按其规范执行。",
		newAgent: func() *agent.Agent {
			box := tmpl.Clone() // 会话级状态 → 会话里现造
			return agent.New(cc, agent.WithTools(box), agent.WithMaxSteps(6))
		},
	}
}

// ───────────────────────── 服务 2：只挂 CLI ─────────────────────────
//
// 命令行程序即工具：模型给 argv 数组，没有 shell 注入面；git 用
// AllowFirstArg 限成只读子命令。工具无状态，Agent 进程级构造一次、全员共享。

func cliService(cc *chat.Client) *service {
	tools := tool.NewSet(
		cli.Command("date", "打印当前日期时间。"),
		cli.Command("df", "查看磁盘用量。", cli.BaseArgs("-h"), cli.Timeout(5*time.Second)),
		cli.Command("git", "查询 git 仓库状态（只读）。",
			cli.AllowFirstArg("status", "log", "diff", "show"),
			cli.Timeout(10*time.Second), cli.MaxOutput(16<<10)),
	)
	shared := agent.New(cc, agent.WithTools(tools), agent.WithMaxSteps(6))
	return &service{
		name:     "cli",
		system:   "你是运维助手，用命令行工具查证后回答，不要凭空猜测机器状态。",
		newAgent: func() *agent.Agent { return shared },
	}
}

// ───────────────────────── 服务 3：只挂 MCP ─────────────────────────
//
// 启动时对每个配置的 server 执行 Dial（长连接，进程级共享），所有远端工具
// 汇入一个 tool.Set。WithToolPrefix 用 server 名做命名空间，防止同名冲突。

func mcpService(cc *chat.Client, cfg *Config) (*service, error) {
	if len(cfg.MCP) == 0 {
		return nil, errors.New("config.mcp 为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tools := tool.NewSet()
	for _, s := range cfg.MCP {
		var ep mcp.Endpoint
		switch {
		case s.URL != "":
			ep = mcp.HTTP(s.URL)
		case s.Command != "":
			ep = mcp.Stdio(s.Command, s.Args...)
		default:
			return nil, fmt.Errorf("mcp %q: 需要 url 或 command", s.Name)
		}
		client, err := mcp.Dial(ctx, ep, mcp.WithToolPrefix(s.Name+"_"))
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", s.Name, err)
		}
		remote, err := client.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("list tools %s: %w", s.Name, err)
		}
		for _, t := range remote {
			tools.Add(t)
		}
		log.Printf("mcp %s: %s v%s, %d tools", s.Name,
			client.ServerInfo().Name, client.ServerInfo().Version, len(remote))
	}

	shared := agent.New(cc, agent.WithTools(tools), agent.WithMaxSteps(8))
	return &service{
		name:     "mcp",
		system:   "你可以使用一组外部工具（MCP），需要时调用，引用工具返回的事实作答。",
		newAgent: func() *agent.Agent { return shared },
	}, nil
}

// ───────────────────────── 服务 4：多 Agent 协同 ─────────────────────────
//
// 没有"编排框架"：子 Agent 经 agent.AsTool 包成普通工具挂给协调者，
// function calling 一路到底。seed 每次委派建全新 Transcript，任务间零串扰。
//
//	协调者
//	 ├── search    搜索专员：只有 web_search 一个工具（示例用假数据，
//	 │             生产上换成 pkg/mcp 拨到搜索类 MCP server 的工具集）
//	 └── librarian 资料专员：只有 skills 工具箱，负责按规范加工内容

func teamService(cc *chat.Client, cfg *Config) *service {
	seed := func(system string) func() (agent.Transcript, error) {
		return func() (agent.Transcript, error) {
			return agent.NewTranscript(adapter.OpenAI, &openai.Request{
				Model:    cfg.Model,
				Messages: []openai.Message{openai.SystemMessage(system)},
			})
		}
	}

	// —— 搜索专员 ——
	type searchArgs struct {
		Query string `json:"query" desc:"要检索的关键词"`
	}
	webSearch := tool.Func("web_search", "检索网络并返回摘要。", tool.Reflect(searchArgs{}),
		func(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
			var a searchArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			// 演示用的假搜索。接真实搜索时删掉这个函数，
			// 把 mcp.Dial 拿到的 ToolSet 挂给 searcher 即可，其余不变。
			return tool.Textf("关于 %q 的检索结果（演示数据）：\n"+
				"1. 官方发布说明——列出了本次版本的语言与工具链变化。\n"+
				"2. 社区评测——性能对比与迁移注意事项。", a.Query), nil
		})
	searcher := agent.New(cc, agent.WithTools(tool.NewSet(webSearch)), agent.WithMaxSteps(4))
	searcherSeed := seed("你是搜索专员。先用 web_search 查证，再用要点归纳结果，不要编造。")

	// —— 资料专员：复用 skills 目录，工具箱整箱挂上 ——
	skills, err := skill.LoadDir(cfg.SkillsDir)
	if err != nil {
		log.Fatalf("load skills: %v", err)
	}
	libraryBox := toolbox.New()
	libraryBox.AddSkills(skills)
	librarian := agent.New(cc, agent.WithTools(libraryBox), agent.WithMaxSteps(4))
	librarianSeed := seed("你是资料专员。打开工具箱里合适的技能，严格按技能规范加工输入内容。")

	// —— 协调者：两个专员就是两个工具 ——
	team := tool.NewSet(
		agent.AsTool("search", "把需要查证的事实性问题委派给搜索专员，传入完整问题。", searcher, searcherSeed),
		agent.AsTool("librarian", "把需要按规范加工的内容委派给资料专员（翻译、周报等）。", librarian, librarianSeed),
	)
	shared := agent.New(cc, agent.WithTools(team), agent.WithMaxSteps(8))
	return &service{
		name:     "team",
		system:   "你是协调者。把子任务委派给合适的专员，自己只负责拆解与汇总。",
		newAgent: func() *agent.Agent { return shared },
	}
}

// ═════════════════════ 自有会话 API：会话管理 ═════════════════════
//
// SDK 刻意不做持久化，会话就是你业务里的一个 map。三条规矩：
//
//  1. Transcript 不支持并发 —— 每个会话一把锁，同一会话的并发请求直接 409。
//  2. Agent 构造零成本 —— 每会话独立工具状态时连 Agent 一起现造（见 service.newAgent）。
//  3. 记得清理 —— 这里用最朴素的 TTL janitor，生产上换成你自己的存储策略。

type session struct {
	mu       sync.Mutex // TryLock 失败 = 该会话正在跑
	tr       agent.Transcript
	ag       *agent.Agent
	lastSeen time.Time
}

type sessionStore struct {
	mu   sync.Mutex
	m    map[string]*session
	make func() (*session, error)
}

func newStore(cfg *Config, svc *service) *sessionStore {
	s := &sessionStore{m: map[string]*session{}}
	s.make = func() (*session, error) {
		tr, err := agent.NewTranscript(adapter.OpenAI, &openai.Request{
			Model:    cfg.Model,
			Messages: []openai.Message{openai.SystemMessage(svc.system)},
		})
		if err != nil {
			return nil, err
		}
		return &session{tr: tr, ag: svc.newAgent()}, nil
	}
	go func() { // TTL janitor：半小时没动静的会话直接丢
		for range time.Tick(5 * time.Minute) {
			s.mu.Lock()
			for id, sess := range s.m {
				if time.Since(sess.lastSeen) > 30*time.Minute {
					delete(s.m, id)
				}
			}
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *sessionStore) get(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.m[id]; ok {
		sess.lastSeen = time.Now()
		return sess, nil
	}
	sess, err := s.make()
	if err != nil {
		return nil, err
	}
	sess.lastSeen = time.Now()
	s.m[id] = sess
	return sess, nil
}

// ═════════════════════ 自有会话 API：HTTP 层 ═════════════════════

type chatRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
	Message        string `json:"message"`
	Stream         bool   `json:"stream,omitempty"`
}

type chatResponse struct {
	ConversationID string     `json:"conversation_id"`
	Reply          string     `json:"reply"`
	Steps          int        `json:"steps"`
	Usage          chat.Usage `json:"usage"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, a ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, a...)})
}

// sessionHandler 把一个 sessionStore 变成 HTTP 端点。四个服务共用这一个
// 函数——服务差异全部在 service 定义里，HTTP 层完全同构。
func sessionHandler(store *sessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		var req chatRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json: %v", err)
			return
		}
		if req.Message == "" {
			writeErr(w, http.StatusBadRequest, "message is required")
			return
		}
		if req.ConversationID == "" {
			req.ConversationID = fmt.Sprintf("c-%d", time.Now().UnixNano())
		}

		sess, err := store.get(req.ConversationID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "create session: %v", err)
			return
		}
		if !sess.mu.TryLock() { // 规矩 1：同一会话串行
			writeErr(w, http.StatusConflict, "conversation %s is busy", req.ConversationID)
			return
		}
		defer sess.mu.Unlock()

		sess.tr.User(req.Message)
		if req.Stream {
			streamRun(w, r.Context(), sess, req.ConversationID)
			return
		}

		out, err := sess.ag.Run(r.Context(), sess.tr)
		if err != nil {
			// ErrMaxSteps 时 out 仍带着已完成的步骤，按需返回部分结果
			if errors.Is(err, agent.ErrMaxSteps) {
				writeErr(w, http.StatusBadGateway, "max steps exceeded after %d steps", len(out.Steps))
				return
			}
			writeErr(w, http.StatusBadGateway, "run: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, chatResponse{
			ConversationID: req.ConversationID,
			Reply:          out.Text(),
			Steps:          len(out.Steps),
			Usage:          out.Usage,
		})
	}
}

// streamRun 把 RunStream 的归一化 chunk 翻译成 SSE。事件名与 chunk kind
// 一一对应，data 一律是 JSON，前端好写。
func streamRun(w http.ResponseWriter, ctx context.Context, sess *session, convID string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, err := sess.ag.RunStream(ctx, sess.tr)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "stream: %v", err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	emit := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	emit("start", map[string]string{"conversation_id": convID})

	for c := range ch {
		switch c.Kind {
		case chat.ChunkText:
			delta, _ := chat.ChunkResult[string](&c)
			emit("text", map[string]string{"delta": delta})
		case chat.ChunkThinking:
			delta, _ := chat.ChunkResult[string](&c)
			emit("thinking", map[string]string{"delta": delta})
		case chat.ChunkToolCall:
			frag, _ := chat.ChunkResult[*chat.ToolCallChunk](&c)
			emit("tool_call", frag) // 片段原样透传，按 index 拼 args_delta
		case agent.ChunkToolResult:
			ret, _ := agent.AsToolReturn(&c)
			emit("tool_result", map[string]any{
				"name":     ret.Call.Name,
				"content":  ret.Result.Content,
				"is_error": ret.Result.IsError,
			})
		case chat.ChunkUsage:
			u, _ := chat.ChunkResult[*chat.Usage](&c)
			emit("usage", u)
		case chat.ChunkError:
			err, _ := chat.ChunkResult[error](&c)
			emit("error", map[string]string{"message": err.Error()})
			return
		}
	}
	emit("done", map[string]string{"conversation_id": convID})
}

// ═════════════════════════ 装配 ═════════════════════════

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	// 进程级：一个 chat.Client 服务所有请求。
	cc := chat.New()
	if err := cc.Use(adapter.OpenAI, openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL}); err != nil {
		log.Fatal(err)
	}

	services := map[string]*service{}
	for _, svc := range []*service{skillsService(cc, cfg), cliService(cc), teamService(cc, cfg)} {
		services[svc.name] = svc
	}
	if svc, err := mcpService(cc, cfg); err != nil {
		log.Printf("mcp 服务未启用: %v", err)
	} else {
		services[svc.name] = svc
	}

	mux := http.NewServeMux()

	// ① 标准 OpenAI 兼容层：现成客户端直连（见 openai_api.go）。
	registerOpenAI(mux, cfg, services)

	// ② 自有会话 API：服务端管历史。
	for name, svc := range services {
		mux.HandleFunc("/chat/"+name, sessionHandler(newStore(cfg, svc)))
	}

	log.Printf("listening on %s", cfg.Addr)
	log.Printf("OpenAI 兼容入口: http://localhost%s/v1  可用模型: skills / cli / mcp / team", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, mux))
}
