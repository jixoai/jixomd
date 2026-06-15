package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/core"
)

// WatchOptions configures the watch loop.
type WatchOptions struct {
	InputPath string        // source document
	OutputPath string       // generated output; "" => <input>.gen.md
	BaseDir   string        // resolution root (also the watch root)
	MaxDepth  int
	Debounce  time.Duration // coalesce bursty events; default 200ms
	Out       io.Writer     // status/log output
}

// defaultOutputName derives "<input>.gen.md" from the input name.
func defaultOutputName(input string) string {
	ext := filepath.Ext(input)
	stem := input[:len(input)-len(ext)]
	return stem + ".gen.md"
}

// Watch runs an initial build, then re-builds on any filesystem change under
// BaseDir (coarse-grained, per SPEC §5: any input change → full re-expand).
// It blocks until ctx is cancelled. Each build writes OutputPath.
//
// The watch root is BaseDir (recursive walk + Add). This is deliberately
// coarse: we don't track per-directive access sets (TODO §5.2 optimization).
func Watch(ctx context.Context, opts WatchOptions) error {
	if opts.Debounce == 0 {
		opts.Debounce = 200 * time.Millisecond
	}
	if opts.OutputPath == "" {
		opts.OutputPath = defaultOutputName(opts.InputPath)
	}
	logw := opts.Out
	if logw == nil {
		logw = os.Stderr
	}

	build := func() error {
		b, err := os.ReadFile(opts.InputPath)
		if err != nil {
			return err
		}
		expanded, err := core.Expand(ctx, local.New(opts.BaseDir), string(b), core.Options{
			BaseDir:  opts.BaseDir,
			MaxDepth: opts.MaxDepth,
		})
		if err != nil {
			return err
		}
		return os.WriteFile(opts.OutputPath, []byte(expanded), 0o644)
	}

	// Initial build.
	if err := build(); err != nil {
		return err
	}
	fmt.Fprintf(logw, "jixomd: watching %s → %s\n", opts.InputPath, opts.OutputPath)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// Recursively add directories under BaseDir.
	addRecursive := func(root string) {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				_ = w.Add(p)
			}
			return nil
		})
	}
	addRecursive(opts.BaseDir)

	// Debounce timer: coalesce bursty events into a single rebuild.
	var mu sync.Mutex
	var timer *time.Timer
	scheduleRebuild := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(opts.Debounce, func() {
			if err := build(); err != nil {
				fmt.Fprintf(logw, "jixomd: rebuild error: %v\n", err)
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// If a new directory appears, watch it too.
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			scheduleRebuild()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(logw, "jixomd: watch error: %v\n", err)
		}
	}
}
