package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Long runs. Three knobs keep a many-step run affordable and graceful:
//
//   - WithToolResultWindow stops the context from growing without bound: old
//     tool output is cut down before each model turn.
//   - WithStepGate lets the host end the run early (out of credit, out of time).
//   - WithWrapUp turns "budget exhausted" from an error into one last
//     tool-less turn in which the model reports where it got to.

// ToolResultEliding is implemented by transcripts that can shrink old tool
// results in place. Every built-in transcript implements it.
type ToolResultEliding interface {
	// ElideToolResults leaves the newest keep tool results untouched and cuts
	// every older one down with Elide(content, max).
	ElideToolResults(keep, max int)
}

// WithToolResultWindow keeps the newest keep tool results verbatim and cuts
// every older one to about max bytes (head + tail) before each model turn.
// The model can always re-run a tool — or re-read a file — to see the rest.
// Transcripts that do not implement ToolResultEliding are left alone.
func WithToolResultWindow(keep, max int) Option {
	return func(a *Agent) {
		if keep >= 0 && max > 0 {
			a.resultKeep, a.resultMax = keep, max
		}
	}
}

// WithStepGate consults gate before every step after the first. Returning
// false ends the loop as though the step budget had run out (so WithWrapUp,
// if set, still runs).
func WithStepGate(gate func(step int) bool) Option {
	return func(a *Agent) { a.gate = gate }
}

// WithWrapUp makes an exhausted budget end gracefully: prompt is appended as a
// user message and one more turn runs with no tools advertised, so the model
// can only answer — typically "here is what is done, here is what is left".
// The run then ends without ErrMaxSteps. OnCompletion sees that turn with a
// step number one past the last tool-using step.
func WithWrapUp(prompt string) Option {
	return func(a *Agent) { a.wrapUp = prompt }
}

func (a *Agent) elide(tr Transcript) {
	if a.resultMax <= 0 {
		return
	}
	if e, ok := tr.(ToolResultEliding); ok {
		e.ElideToolResults(a.resultKeep, a.resultMax)
	}
}

func (a *Agent) proceed(step int) bool {
	return step == 1 || a.gate == nil || a.gate(step)
}

const elidedMark = "[arcus: tool output elided"

// Elide keeps about the first and last max/2 bytes of s, cut on rune
// boundaries, and replaces the middle with a note. Both ends survive because
// a command's errors are usually at the bottom and a file's shape at the top.
//
// It is idempotent: an already elided string comes back unchanged, so the
// rewritten history stays byte-stable from one step to the next.
func Elide(s string, max int) string {
	if len(s) <= max || strings.Contains(s, elidedMark) {
		return s
	}
	i := max / 2
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	j := len(s) - max/2
	for j < len(s) && !utf8.RuneStart(s[j]) {
		j++
	}
	return fmt.Sprintf("%s\n…%s: %d of %d bytes dropped; re-run the tool if you need them]…\n%s",
		s[:i], elidedMark, j-i, len(s), s[j:])
}

// ElideTail walks n messages newest-first and applies Elide to every tool
// result except the newest keep. result(i) returns a pointer to message i's
// tool-result text, or nil when message i is not a tool result. Transcripts
// call it from ElideToolResults.
func ElideTail(n int, result func(i int) *string, keep, max int) {
	seen := 0
	for i := n - 1; i >= 0; i-- {
		c := result(i)
		if c == nil {
			continue
		}
		if seen++; seen > keep {
			*c = Elide(*c, max)
		}
	}
}
