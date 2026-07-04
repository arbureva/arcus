// Package skill packages reusable capabilities as prompt + tools bundles. A
// Skill is nothing exotic: a name and one-line description (what the model
// sees up front), long-form Instructions (what the model reads once it decides
// to use the skill), and optionally some tools that belong to it. On the wire
// it all collapses back to function calling, which is why a skill plugs
// straight into tool.Set (via AsTool) or toolbox.Box (via AddSkill, with
// progressive disclosure).
//
// Skills are built two ways — in code:
//
//	s, _ := skill.New(skill.Skill{
//	    Name:         "invoice",
//	    Description:  "Generate PDF invoices that follow company policy.",
//	    Instructions: "……完整的开票规范……",
//	    Tools:        []tool.Tool{renderPDF},
//	})
//
// or from disk, one directory per skill, each holding a SKILL.md with a
// front-matter header (the Anthropic skills layout):
//
//	skills/
//	  invoice/
//	    SKILL.md      --- name: invoice / description: ... --- + body
//	    template.html
//
//	all, _ := skill.LoadDir("./skills")
//	box.AddSkills(all)
package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Arbureva/ice-adk/pkg/tool"
)

// Skill is a reusable capability: metadata the model always sees, instructions
// it loads on demand, and tools that come alive with it.
type Skill struct {
	// Name identifies the skill. Required. It doubles as the namespace / tool
	// name when the skill is injected, so keep it model-friendly:
	// lowercase, digits, - and _.
	Name string `json:"name"`

	// Description is the one-line summary advertised before the skill is
	// loaded. Required — it is the only signal the model has for deciding
	// whether the skill is relevant.
	Description string `json:"description"`

	// Instructions is the long-form body delivered to the model when it loads
	// the skill (the SKILL.md body when loaded from disk).
	Instructions string `json:"instructions,omitempty"`

	// Tools are optional tools bundled with the skill.
	Tools []tool.Tool `json:"-"`

	// Dir is the source directory when the skill was loaded from disk; "" for
	// skills built in code. Resource helpers (ReadFile, FileTool) resolve
	// relative to it.
	Dir string `json:"dir,omitempty"`

	// Meta carries any extra front-matter keys verbatim.
	Meta map[string]string `json:"meta,omitempty"`
}

// New validates s and returns it as a *Skill.
func New(s Skill) (*Skill, error) {
	if err := validName(s.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Description) == "" {
		return nil, fmt.Errorf("skill %q: description is required", s.Name)
	}
	return &s, nil
}

// MustNew is New but panics on error — for skills wired at startup, where a
// bad definition is a programming error.
func MustNew(s Skill) *Skill {
	sk, err := New(s)
	if err != nil {
		panic(err)
	}
	return sk
}

func validName(name string) error {
	if name == "" {
		return errors.New("skill: name is required")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("skill %q: name may only contain letters, digits, - and _", name)
		}
	}
	return nil
}

// AsTool exposes the skill as a single argument-less tool: the definition
// carries Name and Description, and invoking it returns Instructions. This is
// the flat form of progressive disclosure for callers not using toolbox.Box
// (note that bundled Tools are NOT auto-injected in this form — a plain
// tool.Set cannot grow mid-conversation; use toolbox.Box for that).
func (s *Skill) AsTool() tool.Tool {
	name := s.Name
	desc := s.Description + " Call this tool to load the skill's full instructions before using it."
	return tool.Func(name, desc, nil,
		func(context.Context, json.RawMessage) (*tool.Result, error) {
			if s.Instructions == "" {
				return tool.Textf("Skill %q has no further instructions; proceed with its description.", name), nil
			}
			return tool.Text(s.Instructions), nil
		})
}

// ReadFile reads a resource file bundled with a disk-loaded skill. rel is
// resolved against Dir and confined to it — path escapes return an error.
func (s *Skill) ReadFile(rel string) ([]byte, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (s *Skill) resolve(rel string) (string, error) {
	if s.Dir == "" {
		return "", fmt.Errorf("skill %q: not loaded from disk, no resource directory", s.Name)
	}
	root, err := filepath.Abs(s.Dir)
	if err != nil {
		return "", err
	}
	p := filepath.Clean(filepath.Join(root, rel))
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("skill %q: path %q escapes the skill directory", s.Name, rel)
	}
	return p, nil
}

type readFileArgs struct {
	Path string `json:"path" desc:"Path of the resource file, relative to the skill directory."`
}

// FileTool returns a read-only tool giving the model access to the skill's
// bundled resource files (templates, references, scripts). Access is confined
// to the skill directory. Typical use is bundling it into the skill itself:
//
//	s.Tools = append(s.Tools, s.FileTool())
func (s *Skill) FileTool() tool.Tool {
	return tool.Func(
		s.Name+"_read_file",
		fmt.Sprintf("Read a resource file bundled with the %q skill.", s.Name),
		tool.Reflect(readFileArgs{}),
		func(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
			var args readFileArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			b, err := s.ReadFile(args.Path)
			if err != nil {
				return tool.Errf("read %q: %v", args.Path, err), nil
			}
			return tool.Text(string(b)), nil
		})
}
