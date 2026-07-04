// Package cli exposes command-line programs to the model as tool.Tool values —
// the third execution boundary next to in-process functions (tool.Func) and
// remote servers (mcp). On the wire it is still function calling: the model
// fills in argv, the host runs the process, stdout/stderr come back as the
// tool result.
//
//	git := cli.Command("git", "Run git against the current repository.",
//	    cli.Workdir("/repo"),
//	    cli.AllowFirstArg("status", "log", "diff", "show", "branch"),
//	    cli.Timeout(30*time.Second))
//
//	set := tool.NewSet(git)
//
// Handing a model a shell is handing it your machine. Prefer Command with
// AllowFirstArg over Shell, run under a dedicated user or container when you
// can, and treat every option here as a mitigation, not a sandbox.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Arbureva/ice-adk/pkg/tool"
)

const (
	defaultTimeout   = 60 * time.Second
	defaultMaxOutput = 32 * 1024
)

type command struct {
	binary    string
	desc      string
	workdir   string
	env       []string
	baseArgs  []string
	allow     map[string]bool
	timeout   time.Duration
	maxOutput int
	shell     bool
	name      string
}

// Option configures Command / Shell.
type Option func(*command)

// Name overrides the advertised tool name (default: the binary's base name,
// or "shell" for Shell).
func Name(name string) Option { return func(c *command) { c.name = name } }

// Workdir sets the working directory for every invocation.
func Workdir(dir string) Option { return func(c *command) { c.workdir = dir } }

// Env sets the child's environment (nil = inherit).
func Env(env []string) Option { return func(c *command) { c.env = env } }

// BaseArgs are fixed arguments always prepended before the model's arguments —
// e.g. cli.Command("kubectl", ..., cli.BaseArgs("--context", "staging")).
func BaseArgs(args ...string) Option {
	return func(c *command) { c.baseArgs = append(c.baseArgs, args...) }
}

// AllowFirstArg restricts the first model-supplied argument (typically the
// subcommand) to an allowlist. Anything else is rejected before the process
// starts and reported to the model as a tool error.
func AllowFirstArg(allowed ...string) Option {
	return func(c *command) {
		if c.allow == nil {
			c.allow = map[string]bool{}
		}
		for _, a := range allowed {
			c.allow[a] = true
		}
	}
}

// Timeout caps each invocation's wall time (default 60s). On expiry the
// process is killed and the model is told so.
func Timeout(d time.Duration) Option {
	return func(c *command) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// MaxOutput caps how many bytes of combined output are returned to the model
// (default 32 KiB); the rest is truncated with a notice.
func MaxOutput(n int) Option {
	return func(c *command) {
		if n > 0 {
			c.maxOutput = n
		}
	}
}

type commandArgs struct {
	Args  []string `json:"args,omitempty" desc:"Command-line arguments, one per element (no shell quoting)."`
	Stdin string   `json:"stdin,omitempty" desc:"Text piped to the process's standard input."`
}

type shellArgs struct {
	Command string `json:"command" desc:"The shell command line to execute."`
	Stdin   string `json:"stdin,omitempty" desc:"Text piped to the process's standard input."`
}

// Command wraps one binary as a tool. The model supplies argv as an array —
// there is no shell in between, so no quoting or injection surface beyond the
// binary's own behaviour.
func Command(binary, description string, opts ...Option) tool.Tool {
	c := &command{binary: binary, desc: description, timeout: defaultTimeout, maxOutput: defaultMaxOutput}
	for _, o := range opts {
		o(c)
	}
	if c.name == "" {
		c.name = strings.Map(sanitizeRune, baseName(binary))
	}
	return tool.Func(c.name, c.desc, tool.Reflect(commandArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var args commandArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			if c.allow != nil {
				if len(args.Args) == 0 || !c.allow[args.Args[0]] {
					return tool.Errf("first argument must be one of: %s", strings.Join(allowedList(c.allow), ", ")), nil
				}
			}
			argv := append(append([]string(nil), c.baseArgs...), args.Args...)
			return c.run(ctx, c.binary, argv, args.Stdin), nil
		})
}

// Shell wraps a shell (`sh -c`) as a tool, giving the model arbitrary command
// lines. This is the most powerful and most dangerous tool in the SDK — only
// use it in environments you would let an untrusted script run in, and say so
// in your own risk assessment, not just this comment.
func Shell(description string, opts ...Option) tool.Tool {
	c := &command{binary: "/bin/sh", desc: description, timeout: defaultTimeout, maxOutput: defaultMaxOutput, shell: true}
	for _, o := range opts {
		o(c)
	}
	if c.name == "" {
		c.name = "shell"
	}
	return tool.Func(c.name, c.desc, tool.Reflect(shellArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var args shellArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			if strings.TrimSpace(args.Command) == "" {
				return tool.Err("command must not be empty"), nil
			}
			return c.run(ctx, c.binary, []string{"-c", args.Command}, args.Stdin), nil
		})
}

func (c *command) run(ctx context.Context, binary string, argv []string, stdin string) *tool.Result {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Dir = c.workdir
	cmd.Env = c.env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := buf.String()
	if len(out) > c.maxOutput {
		out = out[:c.maxOutput] + fmt.Sprintf("\n[output truncated at %d bytes]", c.maxOutput)
	}

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return tool.Errf("command timed out after %s\n%s", c.timeout, out)
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return tool.Errf("exit status %d\n%s", ee.ExitCode(), out)
		}
		return tool.Errf("command failed to start: %v", err)
	}
	if out == "" {
		return tool.Text("(command succeeded with no output)")
	}
	return tool.Text(out)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func sanitizeRune(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		return r
	default:
		return '_'
	}
}

func allowedList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// deterministic error messages
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
