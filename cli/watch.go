package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/core"
	gitignore "github.com/sabhiram/go-gitignore"
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
			// BaseDir = cwd/project root (what `pwd:`/`$PWD` resolve to);
			// DocDir = this source file's dir (default for relative targets),
			// so `[../*.md](@FILE)` means "parent of the .md", not "parent of
			// cwd". This matches doc-mode behavior (see readDocInput).
			expanded, err := core.Expand(ctx, local.New(opts.BaseDir), string(b), core.Options{
				BaseDir:  opts.BaseDir,
				DocDir:   filepath.Dir(inp.Path),
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

	// Recursively add directories under BaseDir, skipping anything matched by a
	// .gitignore (root + nested, git semantics). Without this, watching a repo
	// pulls in node_modules/.git/dist — thousands of dirs that exhaust the
	// kqueue/watch FD budget on macOS and break every rebuild with "too many
	// open files". We log how many were skipped and the dominant rule.
	dirs, _, ignoredSubtree, byRule := collectWatchDirs(opts.BaseDir)
	for _, d := range dirs {
		_ = w.Add(d)
	}
	fmt.Fprintf(logw, "jixomd: watching %d director%s under %s",
		len(dirs), pluralDir(len(dirs)), displayBase(opts.BaseDir))
	if ignoredSubtree > 0 {
		fmt.Fprintf(logw, " (skipped %d ignored by .gitignore%s)",
			ignoredSubtree, dominantRuleSuffix(byRule))
	}
	fmt.Fprintln(logw)

	// ignoreMatcher answers "is this newly-created path ignored?" for the live
	// Create handler, so a freshly-created node_modules/... is not re-added.
	ignoreMatcher := newIgnoreMatcher(opts.BaseDir)

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
			// If a new directory appears, watch it too — unless gitignored.
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					if !ignoreMatcher.ignored(ev.Name) {
						_ = w.Add(ev.Name)
					}
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

// ignoreLayer is one directory's compiled .gitignore plus its path relative to
// the watch root, composed into a stack during the walk.
type ignoreLayer struct {
	rel string // dir path relative to root, slash-separated
	gi  *gitignore.GitIgnore
}

// collectWatchDirs walks root, returning the directories that should be watched
// (those not ignored by any .gitignore) plus statistics for the skip summary.
// Gitignore semantics: each directory's .gitignore applies to it and its
// descendants, composed with all ancestor .gitignores. ".git" is always
// skipped. All counters are best-effort.
//
// `ignored` counts only the top-level ignored directories (subtree roots);
// `ignoredSubtree` counts every directory inside those subtrees, i.e. the total
// number of directories NOT watched because of .gitignore. The summary uses the
// latter so "skipped N" reflects the real reduction in watched dirs.
func collectWatchDirs(root string) (dirs []string, ignored int, ignoredSubtree int, byRule map[string]int) {
	byRule = map[string]int{}
	var stack []ignoreLayer
	// Root layer: compile root .gitignore if present (rel == "" means root).
	stack = append(stack, ignoreLayer{rel: "", gi: compileIgnore(filepath.Join(root, ".gitignore"))})

	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		base := filepath.Base(rel)

		// Always skip .git — never useful to watch.
		if base == ".git" {
			return filepath.SkipDir
		}

		// Pop layers whose depth no longer covers p (we walked out of them). The
		// root layer has rel="" and covers everything, so it is never popped.
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.rel == "" {
				break
			}
			if rel == top.rel || strings.HasPrefix(rel, top.rel+"/") {
				break
			}
			stack = stack[:len(stack)-1]
		}

		// Compose the effective ignore set = all layers on the stack.
		if matchesAnyLayer(stack, rel) {
			ignored++
			sub := countSubtreeDirs(p)
			ignoredSubtree += 1 + sub // this dir + all descendants
			if rule := firstMatchingRule(stack, rel); rule != "" {
				byRule[rule] += 1 + sub
			}
			return filepath.SkipDir
		}

		// Watchable directory. Push a new layer if it has its own .gitignore.
		dirs = append(dirs, p)
		if rel != "" { // nested .gitignore (root already on stack)
			if gi := compileIgnore(filepath.Join(p, ".gitignore")); gi != nil {
				stack = append(stack, ignoreLayer{rel: rel, gi: gi})
			}
		}
		return nil
	})
	return dirs, ignored, ignoredSubtree, byRule
}

// countSubtreeDirs counts the directories strictly beneath dir (excluding dir
// itself). Used to size an ignored subtree for the skip summary.
func countSubtreeDirs(dir string) int {
	var n int
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// compileIgnore compiles a .gitignore file, returning nil if missing/unreadable.
func compileIgnore(path string) *gitignore.GitIgnore {
	gi, err := gitignore.CompileIgnoreFile(path)
	if err != nil {
		return nil
	}
	return gi
}

// matchesAnyLayer reports whether the directory rel is ignored by any stacked
// .gitignore. Directory rules in git are written with a trailing slash
// (e.g. `node_modules/`); go-gitignore's MatchesPath matches such a rule
// against the directory's *contents* (node_modules/foo) but NOT the bare
// directory name (node_modules). To honor git's "ignored dir ⇒ skip subtree"
// optimization we test three forms: rel as-is, rel with a trailing slash, and
// rel treated as a path prefix via a synthetic child.
func matchesAnyLayer(stack []ignoreLayer, rel string) bool {
	if rel == "" {
		return false
	}
	probes := []string{rel, rel + "/"}
	if rel != "." {
		probes = append(probes, rel+"/.")
	}
	for _, l := range stack {
		if l.gi == nil {
			continue
		}
		for _, pr := range probes {
			if l.gi.MatchesPath(pr) {
				return true
			}
		}
	}
	return false
}

// firstMatchingRule returns the source line of the first rule that matches rel
// (as a directory), for the skip-summary's "e.g. rule …" hint. Empty if none.
func firstMatchingRule(stack []ignoreLayer, rel string) string {
	if rel == "" {
		return ""
	}
	probes := []string{rel, rel + "/"}
	if rel != "." {
		probes = append(probes, rel+"/.")
	}
	for _, l := range stack {
		if l.gi == nil {
			continue
		}
		for _, pr := range probes {
			if ok, how := l.gi.MatchesPathHow(pr); ok && how != nil {
				return how.Line
			}
		}
	}
	return ""
}

// ignoreMatcher answers live "is this new path ignored?" queries. It compiles
// .gitignores lazily along the path from root. For the Create handler the
// common case (node_modules re-created) is covered by the root .gitignore, so
// we compile root + each ancestor .gitignore up to the parent of the path.
type ignoreMatcher struct{ root string }

func newIgnoreMatcher(root string) ignoreMatcher { return ignoreMatcher{root: root} }

func (m ignoreMatcher) ignored(absPath string) bool {
	// Build the effective ignore set from root down to the path's parent.
	var layers []*gitignore.GitIgnore
	if gi := compileIgnore(filepath.Join(m.root, ".gitignore")); gi != nil {
		layers = append(layers, gi)
	}
	rel, err := filepath.Rel(m.root, absPath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	// Walk each ancestor dir, accumulating nested .gitignores.
	dir := filepath.Dir(rel)
	for dir != "." && dir != "/" && dir != "" {
		absDir := filepath.Join(m.root, filepath.FromSlash(dir))
		if gi := compileIgnore(filepath.Join(absDir, ".gitignore")); gi != nil {
			layers = append(layers, gi)
		}
		dir = filepath.Dir(dir)
	}
	for _, gi := range layers {
		// Directory rules (trailing slash) match the dir's contents but not the
		// bare name — probe rel, rel+"/", and rel+"/." as in matchesAnyLayer.
		for _, pr := range []string{rel, rel + "/", rel + "/."} {
			if gi.MatchesPath(pr) {
				return true
			}
		}
	}
	return false
}

// pluralDir returns "y"/"ies" to pluralize "director[y/ies]".
func pluralDir(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// dominantRuleSuffix formats the most-cited ignore rule for the summary, e.g.
// `, most by rule "node_modules/"`. Empty when no rule attribution is available.
func dominantRuleSuffix(byRule map[string]int) string {
	if len(byRule) == 0 {
		return ""
	}
	type rc struct {
		rule  string
		count int
	}
	var rows []rc
	for r, c := range byRule {
		rows = append(rows, rc{r, c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].count > rows[j].count })
	top := rows[0]
	return fmt.Sprintf(", most by rule %q", top.rule)
}

// displayBase shows the watch root, preferring a relative-to-cwd form.
func displayBase(base string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, base); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return base
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
