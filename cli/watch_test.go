package cli_test

import (
	"bytes"
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
	stderr *bytes.Buffer
}

// startWatchBackground runs `jixomd <file> --watch ...` in a goroutine against
// the scenario workspace. It returns after a brief wait for the initial build.
// The watch's stderr (status/summary logging) is captured on the handle for
// assertions via stderrShouldContain.
func (t *testCtx) startWatchBackground(cmd string) error {
	args := shellSplit(cmd)
	if len(args) > 0 && args[0] == "jixomd" {
		args = args[1:]
	}
	args = t.injectBase(args)

	var errw bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cli.RunContext(ctx, args, strings.NewReader(t.stdin), &errw, &errw)
	}()
	t.watch = &watchHandle{cancel: cancel, done: done, stderr: &errw}
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

// watchStderrShouldContain asserts the running watch's captured stderr (the
// status/summary log, e.g. the gitignore-skip line) contains `want`.
func (t *testCtx) watchStderrShouldContain(want string) error {
	if t.watch == nil || t.watch.stderr == nil {
		return fmt.Errorf("no background watch is running")
	}
	if !strings.Contains(t.watch.stderr.String(), want) {
		return fmt.Errorf("watch stderr missing %q\nstderr:\n%s", want, t.watch.stderr.String())
	}
	return nil
}

// watchStderrShouldNotContain asserts the running watch's stderr omits `want`.
func (t *testCtx) watchStderrShouldNotContain(want string) error {
	if t.watch == nil || t.watch.stderr == nil {
		return fmt.Errorf("no background watch is running")
	}
	if strings.Contains(t.watch.stderr.String(), want) {
		return fmt.Errorf("watch stderr unexpectedly contains %q\nstderr:\n%s", want, t.watch.stderr.String())
	}
	return nil
}
