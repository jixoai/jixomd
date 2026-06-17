package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/core"
)

// WatchInput is one source document + its output path.
type WatchInput struct {
	Path   string // absolute path to the source .md
	Output string // absolute path to the generated .gen.md
}

// WatchOptions configures the watch loop.
type WatchOptions struct {
	Inputs   []WatchInput // source documents (output-excluded by the caller)
	BaseDir  string
	MaxDepth int
	Debounce time.Duration // coalesce bursty events; default 200ms
	Out      io.Writer     // status/log output
}

// defaultOutputName derives "<input>.gen.md" from the input name.
func defaultOutputName(input string) string {
	ext := filepath.Ext(input)
	stem := input[:len(input)-len(ext)]
	return stem + ".gen.md"
}

// Watch runs an initial build of all inputs, then re-builds on any filesystem
// change under BaseDir (coarse-grained, per SPEC §5). It blocks until ctx is
// cancelled. Each input writes its own OutputPath.
//
// The caller is responsible for excluding output files from the Inputs list
// (see resolveWatchInputs in cli.go) to prevent generation loops.
func Watch(ctx context.Context, opts WatchOptions) error {
	if opts.Debounce == 0 {
		opts.Debounce = 200 * time.Millisecond
	}
	if len(opts.Inputs) == 0 {
		return fmt.Errorf("no inputs to watch")
	}
	logw := opts.Out
	if logw == nil {
		logw = os.Stderr
	}

	build := func() {
		for _, inp := range opts.Inputs {
			b, err := os.ReadFile(inp.Path)
			if err != nil {
				fmt.Fprintf(logw, "jixomd: read %s: %v\n", inp.Path, err)
				continue
			}
			expanded, err := core.Expand(ctx, local.New(opts.BaseDir), string(b), core.Options{
				BaseDir:  opts.BaseDir,
				MaxDepth: opts.MaxDepth,
			})
			if err != nil {
				fmt.Fprintf(logw, "jixomd: expand %s: %v\n", inp.Path, err)
				continue
			}
			if err := os.WriteFile(inp.Output, []byte(expanded), 0o644); err != nil {
				fmt.Fprintf(logw, "jixomd: write %s: %v\n", inp.Output, err)
				continue
			}
		}
	}

	// Initial build.
	build()
	for _, inp := range opts.Inputs {
		rel, _ := filepath.Rel(opts.BaseDir, inp.Path)
		outRel, _ := filepath.Rel(opts.BaseDir, inp.Output)
		fmt.Fprintf(logw, "jixomd: watching %s → %s\n", rel, outRel)
	}

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
		timer = time.AfterFunc(opts.Debounce, build)
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

// expandGlobInputs resolves positional args (which may be glob patterns like
// "*.meta.md") into concrete file paths relative to baseDir. It then computes
// each input's output path and EXCLUDES any path that is itself an output of
// another input — preventing generation loops (e.g. a .gen.md matched by the
// glob being treated as a source on the next cycle).
//
// Returns the filtered list of WatchInput.
func expandGlobInputs(args []string, baseDir string, outputOverride string) []WatchInput {
	// Step 1: resolve all args to concrete files (glob-expand if needed).
	var allPaths []string
	for _, arg := range args {
		if arg == "-" {
			continue // stdin not supported in watch mode
		}
		full := arg
		if !filepath.IsAbs(arg) {
			full = filepath.Join(baseDir, arg)
		}
		// If it contains glob metacharacters, expand.
		if strings.ContainsAny(arg, "*?[") {
			matches, _ := filepath.Glob(full)
			allPaths = append(allPaths, matches...)
		} else {
			// Single file — also try glob in case it's a literal name with no match
			if _, err := os.Stat(full); err == nil {
				allPaths = append(allPaths, full)
			} else {
				matches, _ := filepath.Glob(full)
				allPaths = append(allPaths, matches...)
			}
		}
	}

	// Step 2: compute output paths.
	type pathPair struct {
		input  string
		output string
	}
	pairs := make([]pathPair, 0, len(allPaths))
	outputSet := map[string]bool{}
	for _, p := range allPaths {
		var out string
		if outputOverride != "" {
			// Single output override only makes sense for single input.
			if filepath.IsAbs(outputOverride) {
				out = outputOverride
			} else {
				out = filepath.Join(filepath.Dir(p), outputOverride)
			}
		} else {
			out = filepath.Join(filepath.Dir(p), defaultOutputName(filepath.Base(p)))
		}
		pairs = append(pairs, pathPair{input: p, output: out})
		outputSet[out] = true
	}

	// Step 3: exclude inputs whose path is also an output (loop prevention).
	result := make([]WatchInput, 0, len(pairs))
	for _, pp := range pairs {
		if outputSet[pp.input] {
			continue // this input is itself an output — skip to avoid loops
		}
		result = append(result, WatchInput{Path: pp.input, Output: pp.output})
	}
	return result
}
