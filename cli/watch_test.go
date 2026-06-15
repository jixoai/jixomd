package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jixoai/jixomd/cli"
)

// watchHandle holds a running watch invocation + its cancellation.
type watchHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startWatchBackground runs `jixomd <file> --watch ...` in a goroutine against
// the scenario workspace. It returns after a brief wait for the initial build.
func (t *testCtx) startWatchBackground(cmd string) error {
	args := shellSplit(cmd)
	if len(args) > 0 && args[0] == "jixomd" {
		args = args[1:]
	}
	args = t.injectBase(args)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cli.RunContext(ctx, args, strings.NewReader(t.stdin), os.Stderr, os.Stderr)
	}()
	t.watch = &watchHandle{cancel: cancel, done: done}
	// Allow the initial build + fsnotify registration to settle.
	time.Sleep(200 * time.Millisecond)
	return nil
}

// stopWatch cancels the background watch process and waits for it to exit.
func (t *testCtx) stopWatch() {
	if t.watch != nil {
		t.watch.cancel()
		select {
		case <-t.watch.done:
		case <-time.After(2 * time.Second):
		}
		t.watch = nil
	}
}

// overwriteFile writes content to a workspace-relative path.
func (t *testCtx) overwriteFile(path, content string) error {
	full := filepath.Join(t.workspace, path)
	return os.WriteFile(full, []byte(content), 0o644)
}

// overwriteTwice writes two values rapidly (debounce test).
func (t *testCtx) overwriteTwice(path, first, second string) error {
	full := filepath.Join(t.workspace, path)
	if err := os.WriteFile(full, []byte(first), 0o644); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return os.WriteFile(full, []byte(second), 0o644)
}

// outputContainsWithin polls the output file for `want` within a timeout.
func (t *testCtx) outputContainsWithin(timeout time.Duration, filename, want string) error {
	full := filepath.Join(t.workspace, filename)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(full); err == nil && strings.Contains(string(b), want) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(full)
	return fmt.Errorf("output %q did not contain %q within %v\ncontent:\n%s", filename, want, timeout, string(b))
}

// outputNotContainsWithin polls until the output file no longer contains `want`.
func (t *testCtx) outputNotContainsWithin(timeout time.Duration, filename, want string) error {
	full := filepath.Join(t.workspace, filename)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(full); err == nil && !strings.Contains(string(b), want) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(full)
	return fmt.Errorf("output %q still contains %q after %v\ncontent:\n%s", filename, want, timeout, string(b))
}
