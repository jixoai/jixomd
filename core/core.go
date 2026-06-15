// Package core is the pure jixomd expansion engine.
//
// It has zero filesystem / network / git / time dependencies: all side effects
// flow through the injected fsio.IO. The only non-stdlib dependency is goldmark
// (pure Markdown AST parsing). See SPEC §2 / §6.
package core

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/jixoai/jixomd/grammar"
	fsio "github.com/jixoai/jixomd/io"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gparser "github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Options configures Expand.
type Options struct {
	// BaseDir is the root for relative path resolution (also the default watch
	// root in the CLI). Passed to the backend via convention; core itself only
	// treats it as opaque context.
	BaseDir string
	// MaxDepth bounds recursive injection (directive content that itself
	// contains directives). Defaults to 8. See SPEC §2.3.
	MaxDepth int
}

// dedup tracks which content ids have been emitted in the current Expand
// scope. Scope = one Expand call (one document). SPEC §3.2.
type dedup struct {
	seen map[string]bool
}

func newDedup() *dedup { return &dedup{seen: map[string]bool{}} }

// Expand parses doc, resolves every directive via io, and returns the expanded
// document. Non-directive bytes are preserved verbatim (string splicing by
// source span). Directives inside code spans / HTML comments never parse as
// links, so they are left untouched.
//
// Recursion and dedup are unified at the traversal layer (SPEC §2.3 / §3): the
// first time a content id is seen it is emitted in full AND its content is
// further expanded (recursion); a repeat id becomes a REF and is NOT expanded
// further, which cleanly terminates all deterministic cycles. `!` forces a
// full emission in an isolated dedup scope. MaxDepth is the safety net.
func Expand(ctx context.Context, io fsio.IO, doc string, opts Options) (string, error) {
	cur := stripFrontmatterBody(doc)
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	dd := newDedup()
	for i := 0; i < maxDepth; i++ {
		if err := ctx.Err(); err != nil {
			return cur, err
		}
		out, found, err := expandOnce(ctx, io, cur, dd, maxDepth)
		if err != nil {
			return "", err
		}
		if !found {
			return out, nil
		}
		cur = out
	}
	// MaxDepth exhausted: mark any directive still present. SPEC §2.3.
	return markRemaining(cur)
}

// markRemaining replaces any directive still present in cur with a max-depth
// marker. Used when MaxDepth is exhausted.
func markRemaining(doc string) (string, error) {
	root := newMarkdown().Parser().Parse(text.NewReader([]byte(doc)))
	var dirs []*grammar.Directive
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if d, ok := n.(*grammar.Directive); ok {
			dirs = append(dirs, d)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if len(dirs) == 0 {
		return doc, nil
	}
	subs := make([]substitution, 0, len(dirs))
	for _, d := range dirs {
		subs = append(subs, substitution{
			start:   d.Segment.Start,
			stop:    d.Segment.Stop,
			content: "<!-- jixomd: max depth exceeded -->",
		})
	}
	return splice(doc, subs), nil
}

type substitution struct {
	start, stop int
	content     string
}

// expandOnce resolves one layer of directives in doc. Returns found=false when
// the document contains no directives (fixpoint reached).
func expandOnce(ctx context.Context, io fsio.IO, doc string, dd *dedup, maxDepth int) (string, bool, error) {
	root := newMarkdown().Parser().Parse(text.NewReader([]byte(doc)))

	var dirs []*grammar.Directive
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if d, ok := n.(*grammar.Directive); ok {
			dirs = append(dirs, d)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if len(dirs) == 0 {
		return doc, false, nil
	}

	subs := make([]substitution, 0, len(dirs))
	for _, d := range dirs {
		subs = append(subs, substitution{
			start:   d.Segment.Start,
			stop:    d.Segment.Stop,
			content: resolveDirective(ctx, io, d, dd, maxDepth),
		})
	}
	return splice(doc, subs), true, nil
}

// splice applies substitutions to doc, non-overlapping, back-to-front.
func splice(doc string, subs []substitution) string {
	sort.Slice(subs, func(i, j int) bool { return subs[i].start < subs[j].start })
	var b strings.Builder
	prev := 0
	for _, s := range subs {
		if s.start < prev {
			continue // defensive: skip overlapping (nested) spans
		}
		b.WriteString(doc[prev:s.start])
		b.WriteString(s.content)
		prev = s.stop
	}
	b.WriteString(doc[prev:])
	return b.String()
}

// newMarkdown builds a goldmark instance with the directive inline parser
// registered below the built-in link parser's priority (100 vs 200). goldmark
// tries inline parsers in ascending priority order and takes the first that
// returns a node, so a lower number means "tried first". The directive parser
// wins on '[..](@..)' and returns nil for ordinary '[', deferring to link.
func newMarkdown() goldmark.Markdown {
	return goldmark.New(goldmark.WithParserOptions(
		gparser.WithInlineParsers(
			util.Prioritized(gparser.InlineParser(grammar.NewDirectiveParser()), 100),
		),
	))
}

// resolveDirective maps one directive to its expanded text. Per SPEC §3:
//   - first occurrence of a content id → full block (and content may recurse)
//   - repeat id (no `!`) → REF, no further recursion
//   - `!` → full block in an isolated dedup scope
func resolveDirective(ctx context.Context, io fsio.IO, d *grammar.Directive, dd *dedup, maxDepth int) string {
	switch d.Mode {
	case "FILE", "INJECT":
		paths, err := globPaths(io, d)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		var b strings.Builder
		for _, p := range paths {
			b.WriteString(renderFile(ctx, io, d.Mode, p, d.Params, d.Bang, dd, maxDepth))
		}
		return b.String()
	case "FILE_LIST":
		paths, err := globPaths(io, d)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		id := stableID("FILE_LIST\x00" + d.Target)
		return wrapFull(id, d.Target, renderFileList(paths, d.Params), false)
	case "FILE_TREE":
		paths, err := globPaths(io, d)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		id := stableID("FILE_TREE\x00" + d.Target)
		return wrapFull(id, d.Target, renderFileTree(paths, d.Params), false)
	case "GIT_FILE", "GIT_DIFF":
		return resolveGit(ctx, io, d, dd, maxDepth)
	default:
		return fmt.Sprintf("<!-- jixomd: unknown mode %q -->", d.Mode)
	}
}

// globPaths resolves a directive's target via IO.Glob, forwarding the relevant
// params (gitignore/ignore/ignoreFiles/dot) into GlobOptions.
func globPaths(io fsio.IO, d *grammar.Directive) ([]string, error) {
	opts := fsio.GlobOptions{
		Gitignore:   paramBool(d.Params, "gitignore", true),
		Ignore:      paramStrings(d.Params, "ignore"),
		IgnoreFiles: paramStrings(d.Params, "ignoreFiles"),
		Dot:         paramBool(d.Params, "dot", false),
	}
	return io.Glob(d.Target, opts)
}

// noFoundBlock produces the "no files found" output, honoring noFound.* params.
func noFoundBlock(d *grammar.Directive) string {
	if msg := firstParam(d.Params, "noFound.msg"); msg != "" {
		prefix := firstParam(d.Params, "noFound.prefix")
		suffix := firstParam(d.Params, "noFound.suffix")
		return prefix + msg + suffix
	}
	if paramBool(d.Params, "noFound", false) {
		return ""
	}
	return fmt.Sprintf("<!-- jixomd: no files for %q -->", d.Target)
}

// resolveGit handles @GIT_FILE and @GIT_DIFF. The target is either:
//   - a bare glob/path → working-tree view (changed files matching it)
//   - "ref:path1,path2" → commit view (files at that ref)
//
// If IO.Git() is unsupported, emits a degradation comment.
func resolveGit(ctx context.Context, io fsio.IO, d *grammar.Directive, dd *dedup, maxDepth int) string {
	git, err := io.Git()
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git unsupported -->")
	}

	ref, patterns := parseGitTarget(d.Target)
	staged := paramBool(d.Params, "staged", false)

	if ref == "" {
		// Working-tree view: list changed files matching the patterns.
		changed, err := git.ChangedFiles()
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
		}
		var matched []fsio.GitFile
		for _, f := range changed {
			if matchAny(patterns, f.Path) {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			return noFoundBlock(d)
		}
		var b strings.Builder
		for _, f := range matched {
			b.WriteString(renderGitFile(ctx, io, d.Mode, f, "", staged, git, d.Params, d.Bang, dd, maxDepth))
		}
		return b.String()
	}

	// Commit view: list files at ref matching patterns.
	files, err := git.FilesAtCommit(ref, patterns)
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
	}
	if len(files) == 0 {
		return noFoundBlock(d)
	}
	var b strings.Builder
	for _, p := range files {
		f := fsio.GitFile{Path: p, Status: fsio.GitStatusModified}
		b.WriteString(renderGitFile(ctx, io, d.Mode, f, ref, staged, git, d.Params, d.Bang, dd, maxDepth))
	}
	return b.String()
}

// renderGitFile renders one file's git content/diff with a status-suffixed title.
func renderGitFile(ctx context.Context, io fsio.IO, mode string, f fsio.GitFile, ref string, staged bool, git fsio.Git, params map[string][]string, bang bool, dd *dedup, maxDepth int) string {
	id := stableID(mode + "\x00" + f.Path + "\x00" + ref)
	if bang {
		dd.seen[id] = true
	} else if dd.seen[id] {
		return wrapREF(id, f.Path)
	}
	dd.seen[id] = true

	var content string
	var status fsio.GitStatus
	var err error
	if mode == "GIT_FILE" {
		if ref == "" {
			content, status, err = git.WorkingContent(f.Path, staged)
		} else {
			content, status, err = git.CommitContent(ref, f.Path)
		}
	} else { // GIT_DIFF
		if ref == "" {
			content, status, err = git.WorkingDiff(f.Path, staged)
		} else {
			content, status, err = git.CommitDiff(ref, f.Path)
		}
	}
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git error %q: %v -->", f.Path, err)
	}
	if status == "" {
		status = f.Status
	}
	// Append status to the title for git modes.
	titleParams := map[string][]string{}
	for k, v := range params {
		titleParams[k] = v
	}
	titleParams["filepath"] = []string{f.Path + " (" + string(status) + ")"}
	processed := applyOutputShaping("FILE", f.Path, content, titleParams)
	if mode == "GIT_DIFF" {
		// Diff always uses diff fence regardless of extension.
		lang := firstParam(params, "lang")
		if lang == "" {
			lang = "diff"
		}
		fence := "```"
		if strings.Contains(content, fence) {
			fence = "````"
		}
		processed = fmt.Sprintf("`%s (%s)`\n\n%s%s\n%s\n%s\n", f.Path, status, fence, lang, content, fence)
	}
	return wrapFull(id, f.Path, processed, bang)
}

// parseGitTarget splits "ref:path1,path2" into (ref, []pattern). A bare glob
// (no colon, or colon not acting as ref separator) yields ("", [target]).
func parseGitTarget(target string) (ref string, patterns []string) {
	idx := strings.IndexByte(target, ':')
	if idx <= 0 || idx == len(target)-1 {
		return "", []string{target}
	}
	ref = target[:idx]
	// Heuristic: a ref is short and has no glob/slash wildcards. If the part
	// before ':' looks like a path (contains / or *), treat the whole thing as
	// a working-tree pattern.
	if strings.ContainsAny(ref, "/*\\") {
		return "", []string{target}
	}
	rest := target[idx+1:]
	for _, p := range strings.Split(rest, ",") {
		p = strings.TrimSpace(p)
		if p == "**" || p == "*" {
			patterns = append(patterns, "**")
		} else {
			patterns = append(patterns, p)
		}
	}
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	return ref, patterns
}

// matchAny reports whether path matches any of the patterns (glob).
func matchAny(patterns []string, path string) bool {
	for _, pat := range patterns {
		if pat == "**" {
			return true
		}
		// simple containment or exact match; glob matching delegated to backend
		// in production, but for the working-tree filter a suffix/prefix check
		// suffices because ChangedFiles already returned real paths.
		if pat == path || strings.HasPrefix(path, strings.TrimSuffix(pat, "/**")) || strings.HasSuffix(path, pat) {
			return true
		}
	}
	return false
}

// renderFileList emits one path per line.
func renderFileList(paths []string, params map[string][]string) string {
	prefix := paramPrefix(params)
	var b strings.Builder
	for _, p := range paths {
		name := overridePath(p, params)
		b.WriteString(applyPrefix(prefix, name))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderFileTree emits a tree view using ├──/└── connectors.
func renderFileTree(paths []string, params map[string][]string) string {
	root := firstParam(params, "filepath")
	if root == "" {
		root = "."
	}
	tree := buildTree(paths)
	var b strings.Builder
	b.WriteString(root)
	b.WriteByte('\n')
	renderTreeNode(&b, tree, "")
	return strings.TrimRight(b.String(), "\n")
}

// treeNode is an in-memory tree for FILE_TREE rendering.
type treeNode struct {
	name     string
	children map[string]*treeNode
	isLeaf   bool
}

func newTreeNode(name string) *treeNode {
	return &treeNode{name: name, children: map[string]*treeNode{}}
}

// buildTree constructs a tree from slash-separated paths.
func buildTree(paths []string) *treeNode {
	root := newTreeNode("")
	for _, p := range paths {
		segs := strings.Split(p, "/")
		cur := root
		for i, s := range segs {
			n, ok := cur.children[s]
			if !ok {
				n = newTreeNode(s)
				cur.children[s] = n
			}
			n.isLeaf = i == len(segs)-1
			cur = n
		}
	}
	return root
}

// renderTreeNode writes the tree with box-drawing connectors.
func renderTreeNode(b *strings.Builder, n *treeNode, prefix string) {
	names := make([]string, 0, len(n.children))
	for k := range n.children {
		names = append(names, k)
	}
	sort.Strings(names)
	for i, name := range names {
		child := n.children[name]
		last := i == len(names)-1
		connector := "├── "
		if last {
			connector = "└── "
		}
		label := name
		b.WriteString(prefix)
		b.WriteString(connector)
		b.WriteString(label)
		b.WriteByte('\n')
		if len(child.children) > 0 {
			extension := "│   "
			if last {
				extension = "    "
			}
			renderTreeNode(b, child, prefix+extension)
		}
	}
}

// renderFile produces the START/END-wrapped block for one file, applying dedup.
func renderFile(ctx context.Context, io fsio.IO, mode, path string, params map[string][]string, bang bool, dd *dedup, maxDepth int) string {
	id := stableID(mode + "\x00" + path)

	// `!` forces full content in an isolated dedup scope (SPEC §3.3). Its nested
	// directives resolve in a fresh scope, but the block's own id IS registered
	// in the parent scope so a later ordinary [path](@FILE) can REF it.
	if bang {
		nestedDD := newDedup()
		content, _ := io.ReadFile(path)
		processed := applyOutputShaping(mode, path, string(content), params)
		if mode == "INJECT" {
			processed = expandInjected(ctx, io, processed, nestedDD, maxDepth)
		}
		dd.seen[id] = true
		return wrapFull(id, path, processed, true)
	}

	// Repeat (no `!`): emit REF, do not recurse. Terminates cycles.
	if dd.seen[id] {
		return wrapREF(id, path)
	}
	dd.seen[id] = true

	content, rerr := io.ReadFile(path)
	if rerr != nil {
		return fmt.Sprintf("<!-- jixomd: read error %q: %v -->", path, rerr)
	}
	processed := applyOutputShaping(mode, path, string(content), params)
	// INJECT content may itself contain directives → expand them in the same
	// dedup scope so cross-file cycles still terminate.
	if mode == "INJECT" {
		processed = expandInjected(ctx, io, processed, dd, maxDepth)
	}
	return wrapFull(id, path, processed, false)
}

// expandInjected recursively expands directives inside injected content,
// sharing the parent dedup scope (so cycles terminate). Bounded by maxDepth.
func expandInjected(ctx context.Context, io fsio.IO, content string, dd *dedup, maxDepth int) string {
	cur := content
	for i := 0; i < maxDepth; i++ {
		if err := ctx.Err(); err != nil {
			return cur
		}
		out, found, err := expandOnce(ctx, io, cur, dd, maxDepth)
		if err != nil || !found {
			return out
		}
		cur = out
	}
	return cur
}

// wrapFull emits the START/content/END form. SPEC §3.1.
func wrapFull(id, path, content string, bang bool) string {
	startLine := fmt.Sprintf("<!-- jixomd:START id=%s path=%q -->", id, path)
	if bang {
		startLine = fmt.Sprintf("<!-- jixomd:START id=%s path=%q force=1 -->", id, path)
	}
	return startLine + "\n" + content + "\n<!-- jixomd:END id=" + id + " -->\n"
}

// wrapREF emits the self-closing START / REF / END form for a repeat. SPEC §3.1.
func wrapREF(id, path string) string {
	return fmt.Sprintf("<!-- jixomd:START id=%s path=%q /-->\n<!-- jixomd:REF -->\n<!-- jixomd:END id=%s -->\n", id, path, id)
}

// applyOutputShaping wraps content with a code fence for FILE mode and applies
// lang/ext/map_ext_*/prefix/filepath params. Pure. For INJECT, only prefix is
// applied (content is otherwise verbatim).
func applyOutputShaping(mode, path, content string, params map[string][]string) string {
	prefix := paramPrefix(params)
	out := applyPrefix(prefix, content)
	if mode != "FILE" {
		return out
	}
	lang := langForPath(path, params)
	fence := "```"
	if strings.Contains(content, fence) {
		fence = "````"
	}
	title := overridePath(path, params)
	var b strings.Builder
	fmt.Fprintf(&b, "`%s`\n\n", title)
	fmt.Fprintf(&b, "%s%s\n%s\n%s\n", fence, lang, out, fence)
	return b.String()
}

// --- helpers ---

func extOf(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return ""
}

func firstParam(params map[string][]string, key string) string {
	if v := params[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// paramBool reads a bool param, returning def when absent/empty. Accepts
// "true"/"1"/"yes" as true; everything else false.
func paramBool(params map[string][]string, key string, def bool) bool {
	v := firstParam(params, key)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}

// paramStrings reads a param that may appear once or many times into a slice.
func paramStrings(params map[string][]string, key string) []string {
	return params[key]
}

// paramPrefix resolves the "prefix" param: a string applied verbatim, or a
// number meaning N spaces.
func paramPrefix(params map[string][]string) string {
	v := firstParam(params, "prefix")
	if v == "" {
		return ""
	}
	if n, err := strconv.Atoi(v); err == nil {
		return strings.Repeat(" ", n)
	}
	return v
}

// applyPrefix prepends prefix to every line of s.
func applyPrefix(prefix, s string) string {
	if prefix == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// overridePath returns the filepath param if set, else the original path.
func overridePath(path string, params map[string][]string) string {
	if fp := firstParam(params, "filepath"); fp != "" {
		return fp
	}
	return path
}

// langForPath resolves the code-fence language for a file, honoring
// lang > ext > map_ext_<ext>_lang params, falling back to the file extension.
func langForPath(path string, params map[string][]string) string {
	if lang := firstParam(params, "lang"); lang != "" {
		return lang
	}
	ext := extOf(path)
	if ext == "" {
		ext = path
	}
	if lang := firstParam(params, "ext"); lang != "" {
		return lang
	}
	if lang := firstParam(params, "map_ext_"+ext+"_lang"); lang != "" {
		return lang
	}
	return extOf(path)
}

func stableID(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum32())
}

// stripFrontmatterBody removes a leading YAML frontmatter block (`---` fences`)
// from the document. Full YAML parsing + cwd/output extraction is TODO; this
// minimal stripper only clears the block so its contents are not treated as
// prose. See SPEC §1.5.
func stripFrontmatterBody(doc string) string {
	rest, ok := cutFrontmatterOpen(doc)
	if !ok {
		return doc
	}
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		if i := strings.Index(rest, "\n---\r\n"); i >= 0 {
			idx = i
		}
	}
	if idx < 0 {
		return doc // no closing fence; leave untouched
	}
	end := idx + len("\n---")
	if nl := strings.IndexByte(rest[end:], '\n'); nl >= 0 {
		return rest[end+nl+1:]
	}
	return ""
}

func cutFrontmatterOpen(doc string) (string, bool) {
	if strings.HasPrefix(doc, "---\r\n") {
		return doc[len("---\r\n"):], true
	}
	if strings.HasPrefix(doc, "---\n") {
		return doc[len("---\n"):], true
	}
	return doc, false
}
