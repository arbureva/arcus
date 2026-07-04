// Package toolbox implements progressive disclosure for tool injection. A Box
// splits tools into an always-visible base set and named namespaces that stay
// folded until the model asks for them: only each namespace's one-line summary
// is advertised (inside a single meta-tool's description), and calling the
// meta-tool unfolds the namespace — its tools join the next request and its
// long-form instructions come back as the tool result.
//
// This keeps the tool surface (and the token bill) proportional to what the
// conversation actually needs, instead of front-loading every schema of every
// capability into every request.
//
//	box := toolbox.New()
//	box.Add(clock)                                     // always visible
//	box.Namespace("fs", "Read and write local files.", // folded until used
//	    toolbox.Tools(readFile, writeFile),
//	    toolbox.Instructions("Paths are relative to the project root. ..."))
//	box.AddSkill(pdfSkill)                             // a skill is a namespace
//
//	ag := agent.New(cli, agent.WithTools(box))          // Box satisfies agent.Tools
//
// A Box carries per-conversation state (which namespaces are open), so give
// each concurrent conversation its own Box — build one template and Clone it.
package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Arbureva/ice-adk/pkg/skill"
	"github.com/Arbureva/ice-adk/pkg/tool"
)

// DefaultMetaName is the name of the meta-tool the model calls to unfold a
// namespace.
const DefaultMetaName = "open_toolbox"

type namespace struct {
	name         string
	summary      string // one-liner shown before activation
	instructions string // long-form guidance returned on activation
	tools        []tool.Tool
}

// Box is an agent.Tools implementation with folding namespaces. All methods
// are safe for concurrent use, but the activation state is conversational —
// one Box per running conversation.
type Box struct {
	mu       sync.RWMutex
	metaName string
	base     []tool.Tool
	order    []string
	spaces   map[string]*namespace
	active   map[string]bool
	index    map[string]tool.Tool // name -> tool, base + all namespaces
}

// Option configures New.
type Option func(*Box)

// WithMetaName overrides the meta-tool's name (default "open_toolbox").
func WithMetaName(name string) Option {
	return func(b *Box) {
		if name != "" {
			b.metaName = name
		}
	}
}

// New returns an empty Box.
func New(opts ...Option) *Box {
	b := &Box{
		metaName: DefaultMetaName,
		spaces:   map[string]*namespace{},
		active:   map[string]bool{},
		index:    map[string]tool.Tool{},
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Box) register(t tool.Tool) {
	if t == nil {
		panic("toolbox: nil tool")
	}
	name := t.Definition().Name
	if name == "" {
		panic("toolbox: tool with empty name")
	}
	if name == b.metaName {
		panic("toolbox: tool name collides with meta-tool " + b.metaName)
	}
	if _, dup := b.index[name]; dup {
		panic("toolbox: duplicate tool name " + name)
	}
	b.index[name] = t
}

// Add registers always-visible tools. Like tool.Set, wiring mistakes (nil
// tool, empty or duplicate name) panic.
func (b *Box) Add(tools ...tool.Tool) *Box {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range tools {
		b.register(t)
		b.base = append(b.base, t)
	}
	return b
}

// NamespaceOption configures a namespace.
type NamespaceOption func(*namespace)

// Tools sets the tools that unfold with the namespace.
func Tools(tools ...tool.Tool) NamespaceOption {
	return func(n *namespace) { n.tools = append(n.tools, tools...) }
}

// Instructions sets the long-form guidance returned to the model when the
// namespace is opened. Empty is fine — the model then just gets the tool list.
func Instructions(text string) NamespaceOption {
	return func(n *namespace) { n.instructions = text }
}

// Namespace declares a folded namespace. summary is the one-line description
// the model sees before opening it; keep it short and factual, it is the only
// signal the model has for deciding whether to open the namespace.
func (b *Box) Namespace(name, summary string, opts ...NamespaceOption) *Box {
	if name == "" {
		panic("toolbox: namespace with empty name")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, dup := b.spaces[name]; dup {
		panic("toolbox: duplicate namespace " + name)
	}
	n := &namespace{name: name, summary: summary}
	for _, o := range opts {
		o(n)
	}
	for _, t := range n.tools {
		b.register(t)
	}
	b.spaces[name] = n
	b.order = append(b.order, name)
	return b
}

// AddSkill folds a skill into the box as a namespace: the skill's description
// becomes the pre-activation summary, its instructions are returned on
// activation, and its bundled tools unfold with it.
func (b *Box) AddSkill(s *skill.Skill) *Box {
	if s == nil {
		panic("toolbox: nil skill")
	}
	return b.Namespace(s.Name, s.Description,
		Tools(s.Tools...), Instructions(s.Instructions))
}

// AddSkills is AddSkill over a slice, e.g. the result of skill.LoadDir.
func (b *Box) AddSkills(skills []*skill.Skill) *Box {
	for _, s := range skills {
		b.AddSkill(s)
	}
	return b
}

// Activate opens a namespace programmatically (the model normally does this
// itself through the meta-tool). Unknown names return an error.
func (b *Box) Activate(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.spaces[name]; !ok {
		return fmt.Errorf("toolbox: no namespace %q", name)
	}
	b.active[name] = true
	return nil
}

// Active lists currently-open namespaces in declaration order.
func (b *Box) Active() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.active))
	for _, name := range b.order {
		if b.active[name] {
			out = append(out, name)
		}
	}
	return out
}

// Reset folds every namespace back up.
func (b *Box) Reset() {
	b.mu.Lock()
	b.active = map[string]bool{}
	b.mu.Unlock()
}

// Clone returns a Box sharing the same tools and namespaces but with fresh
// (all-folded) activation state — the way to reuse one wiring across many
// concurrent conversations.
func (b *Box) Clone() *Box {
	b.mu.RLock()
	defer b.mu.RUnlock()
	nb := &Box{
		metaName: b.metaName,
		base:     append([]tool.Tool(nil), b.base...),
		order:    append([]string(nil), b.order...),
		spaces:   make(map[string]*namespace, len(b.spaces)),
		active:   map[string]bool{},
		index:    make(map[string]tool.Tool, len(b.index)),
	}
	for k, v := range b.spaces {
		nb.spaces[k] = v
	}
	for k, v := range b.index {
		nb.index[k] = v
	}
	return nb
}

// RequestTools implements agent.Tools: the base tools, the tools of every open
// namespace, and — if any namespace is still folded — the meta-tool.
func (b *Box) RequestTools() []interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]interface{}, 0, len(b.base)+4)
	for _, t := range b.base {
		out = append(out, t)
	}
	folded := false
	for _, name := range b.order {
		n := b.spaces[name]
		if b.active[name] {
			for _, t := range n.tools {
				out = append(out, t)
			}
		} else {
			folded = true
		}
	}
	if folded {
		out = append(out, &metaTool{box: b})
	}
	return out
}

// Invoke implements agent.Tools, dispatching to the meta-tool, base tools, and
// open namespaces. Tools inside a folded namespace are invokable too — if the
// model somehow names one (e.g. from an earlier turn), refusing would only
// stall the loop.
func (b *Box) Invoke(ctx context.Context, name string, args json.RawMessage) (*tool.Result, error) {
	if name == b.metaName {
		return (&metaTool{box: b}).Invoke(ctx, args)
	}
	b.mu.RLock()
	t, ok := b.index[name]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("toolbox: no tool named %q", name)
	}
	return t.Invoke(ctx, args)
}

// ---------------------------------------------------------------------------
// meta-tool

type metaTool struct{ box *Box }

type metaArgs struct {
	Namespace string `json:"namespace"`
}

// Definition is computed on demand so the advertised namespace catalogue always
// reflects what is still folded.
func (m *metaTool) Definition() tool.Definition {
	b := m.box
	b.mu.RLock()
	defer b.mu.RUnlock()

	var desc strings.Builder
	desc.WriteString("Open a capability namespace to make its tools available and receive its usage instructions. ")
	desc.WriteString("Open a namespace before attempting any task it covers. Available namespaces:\n")
	names := make([]string, 0, len(b.order))
	for _, name := range b.order {
		if b.active[name] {
			continue
		}
		n := b.spaces[name]
		fmt.Fprintf(&desc, "- %s: %s (%d tools)\n", n.name, n.summary, len(n.tools))
		names = append(names, name)
	}
	sort.Strings(names)

	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"namespace": map[string]any{
				"type":        "string",
				"description": "Name of the namespace to open.",
				"enum":        names,
			},
		},
		"required": []string{"namespace"},
	})
	return tool.Definition{
		Name:        b.metaName,
		Description: strings.TrimRight(desc.String(), "\n"),
		Schema:      schema,
	}
}

func (m *metaTool) Invoke(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
	var args metaArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tool.Errf("bad arguments: %v", err), nil
	}
	b := m.box
	b.mu.Lock()
	n, ok := b.spaces[args.Namespace]
	if ok {
		b.active[args.Namespace] = true
	}
	var available []string
	if ok {
		for _, name := range b.order {
			if !b.active[name] {
				available = append(available, name)
			}
		}
	}
	b.mu.Unlock()

	if !ok {
		return tool.Errf("no namespace %q; call %s with one of the listed namespaces", args.Namespace, b.metaName), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Namespace %q is now open.", n.name)
	if len(n.tools) > 0 {
		names := make([]string, 0, len(n.tools))
		for _, t := range n.tools {
			names = append(names, t.Definition().Name)
		}
		fmt.Fprintf(&out, " Tools now available: %s.", strings.Join(names, ", "))
	}
	if n.instructions != "" {
		out.WriteString("\n\n")
		out.WriteString(n.instructions)
	}
	if len(available) > 0 {
		fmt.Fprintf(&out, "\n\n(Still folded: %s)", strings.Join(available, ", "))
	}
	return tool.Text(out.String()), nil
}

var _ tool.Tool = (*metaTool)(nil)
