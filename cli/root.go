package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/contract"
	"github.com/jixoai/jixomd/core"
	"github.com/spf13/cobra"
)

// rootApp carries shared state across the command tree (stdin/out/err, exit
// code, the context for watch cancellation).
type rootApp struct {
	cmd      *cobra.Command
	stdin    io.Reader
	out      io.Writer
	errw     io.Writer
	exitCode int
}

// newRootCmd builds the full Cobra command tree: the root (doc mode) + the
// resolve subcommand + the hidden expand alias.
func newRootCmd(stdin io.Reader, out, errw io.Writer) *rootApp {
	app := &rootApp{stdin: stdin, out: out, errw: errw}

	root := &cobra.Command{
		Use:   "jixomd [paths...]",
		Short: "A pure Markdown expansion engine",
		Long: `Expand directives like [src/**](@FILE) into real content (files, trees, diffs).
Directives are standard Markdown links with an @MODE URL:

  [src/main.go](@FILE)         file content in a code fence
  [notes.md](@INJECT)          inline content (recursively expanded)
  [src/**/*.ts](@FILE_LIST)    matched paths, one per line
  [src/**](@FILE_TREE)         tree view (├── └──)
  [app.ts](@GIT_FILE)          working-tree content + status
  [app.ts](@GIT_DIFF)          unified diff vs HEAD
  [src/**](@GIT_DIFF?base=main)             diff working tree vs main
  [src/**](@GIT_DIFF?compare=main..feature) diff two refs

Full documentation: https://github.com/jixoai/jixomd`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runDoc(cmd.Context(), args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	// Doc-mode flags.
	root.Flags().StringP("output", "o", "", "write output to this path")
	root.Flags().String("out", "", "alias for --output")
	root.Flags().String("base", "", "base directory: what `pwd:`/`$PWD` resolve to (default cwd)")
	root.Flags().Int("max-depth", 0, "max recursive injection depth (0 = default 8)")
	root.Flags().BoolP("watch", "w", false, "watch for changes and re-expand")
	root.Flags().Duration("watch-debounce", 200*time.Millisecond, "debounce window for --watch")

	// Mark --out as hidden alias (it shadows --output).
	root.Flags().MarkHidden("out")

	// Resolve subcommand.
	resolveCmd := &cobra.Command{
		Use:   "resolve [--packed] [--algo gzip|zstd] [--base DIR]",
		Short: "Resolve directives as JSON (tool-call batch mode)",
		Long: `Read a JSON directive array from stdin, resolve each directive, and
output a JSON block array. Always batch (single = array of one).

Supports --packed for base64(gzip|zstd(json)) transport, suitable for
remote tool calls over size-limited channels.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := resolveParams{}
			p.packed, _ = cmd.Flags().GetBool("packed")
			p.algo, _ = cmd.Flags().GetString("algo")
			p.baseFlag, _ = cmd.Flags().GetString("base")
			return app.runResolve(cmd.Context(), p)
		},
	}
	resolveCmd.Flags().Bool("packed", false, "wrap input/output as base64(<algo>(json))")
	resolveCmd.Flags().String("algo", "gzip", "compression algo when --packed: gzip|zstd")
	resolveCmd.Flags().String("base", "", "base directory for path resolution")
	root.AddCommand(resolveCmd)

	// Hidden expand alias (back-compat: jixomd expand <path> == jixomd <path>).
	expandCmd := &cobra.Command{
		Use:    "expand [paths...]",
		Short:  "alias for the bare command (doc mode)",
		Args:   cobra.ArbitraryArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runDoc(cmd.Context(), args)
		},
	}
	expandCmd.Flags().StringP("output", "o", "", "write output to this path")
	expandCmd.Flags().String("base", "", "base directory for path resolution")
	expandCmd.Flags().Int("max-depth", 0, "max recursive injection depth (0 = default 8)")
	expandCmd.Flags().BoolP("watch", "w", false, "watch for changes and re-expand")
	expandCmd.Flags().Duration("watch-debounce", 200*time.Millisecond, "debounce window for --watch")
	root.AddCommand(expandCmd)

	// Skill subcommand (read embedded skill docs).
	root.AddCommand(newSkillCmd(out, errw))

	// MCP subcommand (start MCP server).
	root.AddCommand(newMCPCmd(stdin, out, errw))

	// Customize help without Cobra templates: templates pull in a large
	// reflection-heavy projection path for what is static command text.
	customizeHelp(root, out)

	// Override the help command to handle `jixomd help @FILE` / `@MODES`.
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command|@MODE]",
		Short: "Help about any command or directive mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			if modeHelpHandled(out, args) {
				return nil
			}
			// Default: delegate to standard Cobra help.
			if len(args) > 0 {
				target, _, e := root.Find(args)
				if e == nil && target != nil {
					return target.Help()
				}
			}
			return root.Help()
		},
	})

	app.cmd = root
	return app
}

// runDoc handles doc mode: Markdown in -> Markdown out (raw). SPEC §4.2.
func (app *rootApp) runDoc(ctx context.Context, args []string) error {
	cmd := app.cmd
	flags := cmd.Flags()

	watch, _ := flags.GetBool("watch")
	debounce, _ := flags.GetDuration("watch-debounce")
	output, _ := flags.GetString("output")
	outAlias, _ := flags.GetString("out")
	baseFlag, _ := flags.GetString("base")
	maxDepth, _ := flags.GetInt("max-depth")

	// --out aliases --output (fixes the broken discard in the old code).
	if output == "" && outAlias != "" {
		output = outAlias
	}

	if watch {
		if len(args) == 0 {
			return fmt.Errorf("--watch requires at least one file path or glob")
		}
		watchBase := baseFlag
		if watchBase == "" {
			cwd, _ := filepath.Abs(".")
			watchBase = cwd
		}
		inputs := expandGlobInputs(args, watchBase, output)
		if len(inputs) == 0 {
			return fmt.Errorf("no input files matched (after excluding outputs)")
		}
		opts := WatchOptions{
			Inputs:   inputs,
			BaseDir:  watchBase,
			MaxDepth: maxDepth,
			Debounce: debounce,
			Out:      app.errw,
		}
		wctx, cancel := contextWithSignals(ctx)
		defer cancel()
		if err := Watch(wctx, opts); err != nil && err != context.Canceled {
			app.exitCode = ExitInternal
			return err
		}
		return nil
	}

	// Non-watch: read input (file(s) or stdin).
	base, docDir, doc, code := readDocInput(args, baseFlag, app.stdin, app.errw)
	if code != ExitOK {
		app.exitCode = code
		return nil
	}

	// The backend walks from docDir (the document's directory) so reported paths
	// are relative to the document — same as before. BaseDir is the cwd/--base,
	// the target of `pwd:` / `$PWD`.
	expanded, err := core.Expand(ctx, local.New(docDir), doc, core.Options{
		BaseDir:  base,
		DocDir:   docDir,
		MaxDepth: maxDepth,
	})
	if err != nil {
		fmt.Fprintln(app.errw, "jixomd:", err)
		app.exitCode = ExitInternal
		return nil
	}

	return writeDocOutput(output, expanded, app.out)
}

// readDocInput reads the document from file args or stdin. It returns:
//   - base: the cwd / --base — what `pwd:` and `$PWD` resolve to, and the
//     backend's Base.
//   - docDir: the source document's directory — default base for relative
//     targets (document-relative). Falls back to base for stdin / --base.
//   - doc: the document text.
func readDocInput(args []string, baseFlag string, stdin io.Reader, errw io.Writer) (base, docDir, doc string, code int) {
	cwd, _ := filepath.Abs(".")
	if baseFlag != "" {
		cwd = baseFlag
	}
	if len(args) == 0 || args[0] == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(errw, "jixomd: read stdin:", err)
			return "", "", "", ExitUsage
		}
		return cwd, cwd, string(b), ExitOK
	}
	path := args[0]
	readPath := path
	if baseFlag != "" && !filepath.IsAbs(path) {
		readPath = filepath.Join(baseFlag, path)
	}
	b, err := readFile(readPath)
	if err != nil {
		fmt.Fprintln(errw, "jixomd:", err)
		return "", "", "", ExitUsage
	}
	// Document directory: the source file's dir (document-relative). When
	// --base is given explicitly, honor it as the doc dir too so an explicit
	// base wins for both Base and DocDir.
	dd := cwd
	if abs, err := filepath.Abs(filepath.Dir(readPath)); err == nil {
		dd = abs
	}
	return cwd, dd, string(b), ExitOK
}

func writeDocOutput(output, doc string, out io.Writer) error {
	if output != "" {
		return writeFile(output, []byte(doc))
	}
	fmt.Fprint(out, doc)
	return nil
}

// customizeHelp installs a direct help renderer. Cobra's SetHelpTemplate uses
// text/template reflection for a static projection, which significantly bloats
// the release binary.
func customizeHelp(cmd *cobra.Command, out io.Writer) {
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printCommandHelp(out, cmd)
	})
}

func printCommandHelp(out io.Writer, cmd *cobra.Command) {
	fmt.Fprintf(out, "%s — %s\n\n", cmd.Name(), cmd.Short)
	if cmd.Long != "" {
		fmt.Fprintln(out, cmd.Long)
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintf(out, "  %s\n\n", cmd.UseLine())
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintln(out, "Available Commands:")
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() {
				fmt.Fprintf(out, "  %-12s %s\n", sub.Name(), sub.Short)
			}
		}
		fmt.Fprintln(out)
	}
	if cmd.HasAvailableLocalFlags() {
		fmt.Fprintln(out, "Flags:")
		fmt.Fprint(out, cmd.LocalFlags().FlagUsages())
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Use \"%s [command] --help\" for more information about a command.\n", cmd.CommandPath())
		fmt.Fprintf(out, "Use \"%s help @FILE\" for help on a specific directive mode.\n", cmd.CommandPath())
	}
}

// modeHelpHandler processes `jixomd help @FILE` etc.
// Returns true if the arg was a mode help request (and was handled).
func modeHelpHandled(out io.Writer, args []string) bool {
	if len(args) == 0 {
		return false
	}
	arg := args[0]
	upper := strings.ToUpper(strings.TrimPrefix(arg, "@"))
	if upper == "MODES" {
		printAllModes(out)
		return true
	}
	if strings.HasPrefix(arg, "@") {
		if printModeHelp(out, upper) {
			return true
		}
		// Unknown mode — print all modes as a hint.
		fmt.Fprintf(out, "Unknown mode: @%s\n\n", upper)
		printAllModes(out)
		return true
	}
	return false
}

// resolveBase picks the base dir: explicit flag, else cwd.
func resolveBase(flag, inputDir string) string {
	if flag != "" {
		return flag
	}
	if inputDir != "" {
		return inputDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// resolveBatch is shared by runResolve (moved here from the old location).
func resolveBatch(reqs []contract.Directive, base string) ([]contract.Block, error) {
	io := local.New(base)
	dirs := make([]core.Directive, len(reqs))
	for i, r := range reqs {
		dirs[i] = core.Directive{
			ID:     r.ID,
			Target: r.Target,
			Mode:   r.Directive,
			Bang:   r.Bang,
			Params: r.Params,
		}
	}
	resolved, err := core.ResolveDirectives(context.Background(), io, dirs, core.Options{BaseDir: base})
	if err != nil {
		return nil, err
	}
	blocks := make([]contract.Block, len(resolved))
	for i, rb := range resolved {
		blocks[i] = contract.Block{ID: rb.ID, Block: rb.Block}
	}
	return blocks, nil
}
