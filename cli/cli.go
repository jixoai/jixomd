// Package cli is the testable CLI layer. main (cmd/jixomd) is a thin wrapper
// that calls cli.Run(args, stdin, stdout, stderr) and maps its exit code.
//
// Keeping the logic here (rather than in main) lets the BDD steps drive the
// CLI in-process: no subprocess, no binary build. See features/.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/contract"
	"github.com/jixoai/jixomd/core"
)

// ExitCode values per SPEC §4.3.
const (
	ExitOK         = 0
	ExitUsage      = 2 // parse / contract error
	ExitIO         = 3 // resolve IO error
	ExitInternal   = 4
)

// Run executes one CLI invocation. args excludes the program name.
// Returns an exit code; writes human-facing output to out, diagnostics to errw.
func Run(args []string, stdin io.Reader, out, errw io.Writer) int {
	return RunContext(context.Background(), args, stdin, out, errw)
}

// RunContext is like Run but with a caller-supplied context (used by tests to
// cancel --watch). For non-watch invocations the context is only checked
// between operations.
func RunContext(ctx context.Context, args []string, stdin io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		usage(errw)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(out)
		return ExitOK
	case "resolve":
		return runResolve(args[1:], stdin, out, errw)
	case "expand":
		// Back-compat alias: `jixomd expand <path>` == `jixomd <path>`.
		return runDoc(ctx, args[1:], stdin, out, errw)
	default:
		// Bare command: `jixomd <path>` / `jixomd -` (doc mode).
		return runDoc(ctx, args, stdin, out, errw)
	}
}

// runDoc handles doc mode: Markdown in -> Markdown out (raw). SPEC §4.2.
func runDoc(ctx context.Context, args []string, stdin io.Reader, out, errw io.Writer) int {
	// Go's flag package stops parsing flags at the first positional argument.
	// Reorder so flags come first, then positionals — this lets users write
	// `jixomd file.md --watch --base x` naturally.
	args = reorderFlags(args)

	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	fs.SetOutput(errw)
	output := fs.String("output", "", "write output to this path (--out alias)")
	_ = fs.String("out", "", "alias for --output")
	baseFlag := fs.String("base", "", "base directory for path resolution (default: cwd or file's dir)")
	maxDepth := fs.Int("max-depth", 0, "max recursive injection depth (0 = default 8)")
	watch := fs.Bool("watch", false, "watch for changes and re-expand (see SPEC §5)")
	debounce := fs.Duration("watch-debounce", 200*time.Millisecond, "debounce window for --watch (e.g. 50ms, 1s)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *watch {
		if len(fs.Args()) == 0 || fs.Arg(0) == "-" {
			fmt.Fprintln(errw, "jixomd: --watch requires a file path (not stdin)")
			return ExitUsage
		}
		inputPath := fs.Arg(0)
		// Resolve input path relative to --base if given.
		if *baseFlag != "" && !filepath.IsAbs(inputPath) {
			inputPath = filepath.Join(*baseFlag, inputPath)
		}
		// Default output goes next to the input (<input>.gen.md). When --output
		// is a bare name, place it under the input's dir too (so tests using
		// --base get the output in the workspace, not the real cwd).
		outPath := *output
		inputDir := filepath.Dir(inputPath)
		if outPath == "" {
			outPath = filepath.Join(inputDir, defaultOutputName(filepath.Base(inputPath)))
		} else if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(inputDir, outPath)
		}
		opts := WatchOptions{
			InputPath:  inputPath,
			OutputPath: outPath,
			BaseDir:    resolveBase(*baseFlag, inputDir),
			MaxDepth:   *maxDepth,
			Debounce:   *debounce,
			Out:        errw,
		}
		// Use the caller's context (tests cancel it); in the real CLI, Run()
		// passes context.Background() and SIGINT/SIGTERM are handled by the
		// terminal. We derive a child that also cancels on signals.
		wctx, cancel := contextWithSignals(ctx)
		defer cancel()
		if err := Watch(wctx, opts); err != nil && err != context.Canceled {
			fmt.Fprintln(errw, "jixomd:", err)
			return ExitInternal
		}
		return ExitOK
	}

	base, doc, code := readDocInput(fs.Args(), *baseFlag, stdin, errw)
	if code != ExitOK {
		return code
	}

	expanded, err := core.Expand(context.Background(), local.New(base), doc, core.Options{BaseDir: base, MaxDepth: *maxDepth})
	if err != nil {
		fmt.Fprintln(errw, "jixomd:", err)
		return ExitInternal
	}

	return writeDocOutput(*output, expanded, out)
}

// resolveBase picks the base dir: explicit flag, else the input file's dir,
// else cwd.
func resolveBase(flag, inputDir string) string {
	if flag != "" {
		return flag
	}
	if inputDir != "" {
		return inputDir
	}
	cwd, _ := filepath.Abs(".")
	return cwd
}

func readDocInput(args []string, baseFlag string, stdin io.Reader, errw io.Writer) (base, doc string, code int) {
	cwd, _ := filepath.Abs(".")
	if baseFlag != "" {
		cwd = baseFlag
	}
	if len(args) == 0 || args[0] == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(errw, "jixomd: read stdin:", err)
			return "", "", ExitUsage
		}
		return cwd, string(b), ExitOK
	}
	path := args[0]
	// Resolve the input path relative to the base dir (when given), so a bare
	// filename "in.md" is found under --base rather than the real cwd.
	readPath := path
	if baseFlag != "" && !filepath.IsAbs(path) {
		readPath = filepath.Join(baseFlag, path)
	}
	b, err := readFile(readPath)
	if err != nil {
		fmt.Fprintln(errw, "jixomd:", err)
		return "", "", ExitUsage
	}
	if baseFlag == "" {
		if abs, err := filepath.Abs(filepath.Dir(path)); err == nil {
			cwd = abs
		}
	}
	return cwd, string(b), ExitOK
}

func writeDocOutput(output, doc string, out io.Writer) int {
	if output != "" {
		if err := writeFile(output, []byte(doc)); err != nil {
			fmt.Fprintln(out, "jixomd:", err) // diagnostics to errw ideally; kept simple here
			return ExitIO
		}
		return ExitOK
	}
	fmt.Fprint(out, doc)
	return ExitOK
}

// runResolve handles resolve mode: JSON directive array in -> JSON block array
// out. Always JSON; --packed wraps both sides in base64(<algo>(json)). SPEC §4.3.
func runResolve(args []string, stdin io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(errw)
	packed := fs.Bool("packed", false, "wrap input/output as base64(<algo>(json))")
	algo := fs.String("algo", "gzip", "compression algo when --packed: gzip|zstd")
	baseFlag := fs.String("base", "", "base directory for path resolution")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(errw, "jixomd: read stdin:", err)
		return ExitUsage
	}

	var reqs []contract.Directive
	if *packed {
		if err := contract.DecodePacked(string(raw), contract.Algo(*algo), &reqs); err != nil {
			fmt.Fprintln(errw, "jixomd: decode packed:", err)
			return ExitUsage
		}
	} else {
		if err := json.Unmarshal(raw, &reqs); err != nil {
			fmt.Fprintln(errw, "jixomd: parse json:", err)
			return ExitUsage
		}
	}

	blocks, err := resolveBatch(reqs, *baseFlag)
	if err != nil {
		fmt.Fprintln(errw, "jixomd: resolve:", err)
		return ExitIO
	}

	var payload string
	if *packed {
		s, err := contract.EncodePacked(blocks, contract.Algo(*algo))
		if err != nil {
			fmt.Fprintln(errw, "jixomd: encode packed:", err)
			return ExitInternal
		}
		payload = s
	} else {
		b, err := json.Marshal(blocks)
		if err != nil {
			fmt.Fprintln(errw, "jixomd: marshal:", err)
			return ExitInternal
		}
		payload = string(b)
	}
	fmt.Fprintln(out, payload)
	return ExitOK
}

// resolveBatch resolves a directive array under ONE Expand dedup scope (SPEC
// §4.3: dedup crosses entries). It synthesizes a doc containing all directives
// separated by blank lines, runs a single core.Expand (so collapseDuplicates
// sees every directive and dedups across them), then slices each directive's
// expanded block out by sentinel markers.
//
// Each directive is wrapped between sentinel HTML comments that survive
// expansion (they're HTML nodes, exempt from directive parsing). After Expand,
// we split on the sentinels to recover per-directive blocks.
func resolveBatch(reqs []contract.Directive, base string) ([]contract.Block, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	io := local.New(base)

	// Build a synthetic doc: for each request, a sentinel-open line, the
	// directive, then a sentinel-close line. Sentinels are HTML comments so
	// they pass through Expand untouched and let us slice blocks afterward.
	var b strings.Builder
	for i, r := range reqs {
		directive := fmt.Sprintf("[%s](@%s)", r.Target, r.Directive)
		if r.Bang {
			directive = fmt.Sprintf("[%s](@%s!)", r.Target, r.Directive)
		}
		fmt.Fprintf(&b, "<!-- jixomd:BATCH:%d:START -->\n", i)
		b.WriteString(directive)
		b.WriteString("\n<!-- jixomd:BATCH:")
		b.WriteString(fmt.Sprintf("%d:END -->\n\n", i))
	}

	expanded, err := core.Expand(context.Background(), io, b.String(), core.Options{})
	if err != nil {
		return nil, err
	}

	// Slice the expanded text by sentinel pairs.
	blocks := make([]contract.Block, len(reqs))
	for i := range reqs {
		openTag := fmt.Sprintf("<!-- jixomd:BATCH:%d:START -->", i)
		closeTag := fmt.Sprintf("<!-- jixomd:BATCH:%d:END -->", i)
		start := strings.Index(expanded, openTag)
		end := strings.Index(expanded, closeTag)
		if start < 0 || end < 0 || end < start {
			blocks[i] = contract.Block{ID: reqs[i].ID, Block: "<!-- jixomd: resolve error: block not found -->"}
			continue
		}
		block := strings.TrimSpace(expanded[start+len(openTag) : end])
		blocks[i] = contract.Block{ID: reqs[i].ID, Block: block}
	}
	return blocks, nil
}

// usage prints help to w.
func usage(w io.Writer) {
	fmt.Fprint(w, `jixomd — Markdown expansion engine

Usage:
  jixomd <path>                 expand directives; write to stdout
  jixomd <path> --output <out>  write to a file
  jixomd -                      read document from stdin; write to stdout
  jixomd resolve [--packed [--algo gzip|zstd]]   JSON array in, JSON array out
  jixomd expand <path>          (alias for bare command)

Directives look like [src/**](@FILE). Modes: FILE, INJECT.
`)
}
