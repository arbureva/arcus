package cli

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX binaries")
	}
}

func TestCommandRuns(t *testing.T) {
	skipOnWindows(t)
	echo := Command("echo", "Echo arguments.")
	if echo.Definition().Name != "echo" {
		t.Fatalf("name = %q", echo.Definition().Name)
	}
	res, err := echo.Invoke(context.Background(), json.RawMessage(`{"args":["hello","world"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Content) != "hello world" || res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestCommandStdinAndBaseArgs(t *testing.T) {
	skipOnWindows(t)
	c := Command("tr", "Translate characters.", BaseArgs("a-z", "A-Z"))
	res, err := c.Invoke(context.Background(), json.RawMessage(`{"stdin":"abc"}`))
	if err != nil || strings.TrimSpace(res.Content) != "ABC" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestAllowFirstArg(t *testing.T) {
	skipOnWindows(t)
	git := Command("echo", "pretend git", Name("git"), AllowFirstArg("status", "log"))
	res, _ := git.Invoke(context.Background(), json.RawMessage(`{"args":["push","--force"]}`))
	if !res.IsError || !strings.Contains(res.Content, "log, status") {
		t.Fatalf("allowlist not enforced: %+v", res)
	}
	res, _ = git.Invoke(context.Background(), json.RawMessage(`{"args":["status"]}`))
	if res.IsError {
		t.Fatalf("allowed arg rejected: %+v", res)
	}
}

func TestNonZeroExitIsModelVisible(t *testing.T) {
	skipOnWindows(t)
	f := Command("false", "Always fails.")
	res, err := f.Invoke(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "exit status 1") {
		t.Fatalf("result = %+v", res)
	}
}

func TestTimeout(t *testing.T) {
	skipOnWindows(t)
	s := Command("sleep", "Sleep.", Timeout(100*time.Millisecond))
	start := time.Now()
	res, err := s.Invoke(context.Background(), json.RawMessage(`{"args":["5"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not kill the process promptly")
	}
}

func TestMaxOutput(t *testing.T) {
	skipOnWindows(t)
	y := Shell("Run shell.", MaxOutput(64), Timeout(5*time.Second))
	res, err := y.Invoke(context.Background(), json.RawMessage(`{"command":"printf 'x%.0s' $(seq 1 500)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "[output truncated at 64 bytes]") {
		t.Fatalf("no truncation notice: %q", res.Content)
	}
}
