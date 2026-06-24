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
	"path"
	"regexp"
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
	// BaseDir is the project root / cwd — what `pwd:` targets and `$PWD`
	// expand to. Also the default watch root in the CLI. Path resolution rules
	// are documented in SPEC (§ Path resolution).
	BaseDir string
	// DocDir is the directory of the source document. Relative directive
	// targets resolve against DocDir by default (document-relative, matching
	// Markdown/HTML convention), so a `.md` file is portable across cwds. When
	// empty, relative targets fall back to BaseDir. Use `pwd:` or `$PWD` to
	// force BaseDir-relative resolution explicitly.
	DocDir string
	// MaxDepth bounds recursive injection (directive content that itself
	// contains directives). Defaults to 8. See SPEC §2.3.
	MaxDepth int
}

// Directive is the structured form of a resolve request, mirroring
// contract.Directive without core depending on the wire/contract package. It
// lets batch callers pass params directly — no markdown round-trip that would
// drop them (issue 001).
type Directive struct {
	ID     string
	Target string
	Mode   string // FILE, INJECT, FILE_LIST, FILE_TREE, GIT_FILE, GIT_DIFF
	Bang   bool
	Params map[string][]string
}

// ResolvedBlock is one directive's expanded output (with START/END markers).
type ResolvedBlock struct {
	ID    string
	Block string
}

// ResolveDirectives resolves a batch of structured directives under ONE dedup
// scope (SPEC §4.3: dedup crosses entries). Unlike the markdown-synthesizing
// path, it preserves Params directly, so output-shaping and glob-control
// params from the wire contract are honored (issue 001). Returns a non-nil
// empty slice for empty input (issue 002).
func ResolveDirectives(ctx context.Context, io fsio.IO, reqs []Directive, opts Options) ([]ResolvedBlock, error) {
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	ro := resolveOpts{baseDir: opts.BaseDir, docDir: opts.DocDir}
	if ro.docDir == "" {
		ro.docDir = opts.BaseDir
	}
	dd := newDedup()
	out := make([]ResolvedBlock, 0, len(reqs)) // non-nil even when empty (issue 002)
	for _, r := range reqs {
		d := &grammar.Directive{
			Target: r.Target,
			Mode:   r.Mode,
			Bang:   r.Bang,
			Params: r.Params,
		}
		block := resolveDirective(ctx, io, d, dd, maxDepth, ro)
		out = append(out, ResolvedBlock{ID: r.ID, Block: strings.TrimSpace(block)})
	}
	return out, nil
}

// dedup tracks which content ids have been emitted in the current Expand
// scope. Scope = one Expand call (one document). SPEC §3.2.
type dedup struct {
	seen map[string]bool
}

func newDedup() *dedup { return &dedup{seen: map[string]bool{}} }

// resolveOpts is the threaded path-resolution context carried through one
// Expand / ResolveDirectives call. It is a plain struct (not Options) so the
// recursive expandOnce → resolveDirective → globPaths chain does not re-pass
// the full Options. See rebaseTarget for how baseDir/docDir are used.
type resolveOpts struct {
	baseDir string // cwd / project root: target of `pwd:` and `$PWD`
	docDir  string // source document dir: default base for relative targets
}

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
	ro := resolveOpts{baseDir: opts.BaseDir, docDir: opts.DocDir}
	if ro.docDir == "" {
		ro.docDir = opts.BaseDir
	}
	dd := newDedup()
	for i := 0; i < maxDepth; i++ {
		if err := ctx.Err(); err != nil {
			return cur, err
		}
		out, found, err := expandOnce(ctx, io, cur, dd, maxDepth, ro)
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
func expandOnce(ctx context.Context, io fsio.IO, doc string, dd *dedup, maxDepth int, ro resolveOpts) (string, bool, error) {
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
			content: resolveDirective(ctx, io, d, dd, maxDepth, ro),
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
func resolveDirective(ctx context.Context, io fsio.IO, d *grammar.Directive, dd *dedup, maxDepth int, ro resolveOpts) string {
	switch d.Mode {
	case "FILE", "INJECT":
		paths, err := globPaths(io, d, ro)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		var b strings.Builder
		for _, p := range paths {
			b.WriteString(renderFile(ctx, io, d.Mode, p, d.Params, d.Bang, dd, maxDepth, ro))
		}
		return b.String()
	case "FILE_LIST":
		paths, err := globPaths(io, d, ro)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		// Strong id: mode + target + params + resolved file set (issue 003).
		// This makes two list/tree directives dedup only when they resolve to
		// the same content, and never collapse when params differ.
		id := listTreeID(d.Mode, d.Target, d.Params, paths)
		if !d.Bang && dd.seen[id] {
			return wrapREF(id, d.Mode, d.Target)
		}
		dd.seen[id] = true
		return wrapFull(id, d.Mode, d.Target, renderFileList(paths, d.Params), d.Bang)
	case "FILE_TREE":
		paths, err := globPaths(io, d, ro)
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return noFoundBlock(d)
		}
		sort.Strings(paths)
		id := listTreeID(d.Mode, d.Target, d.Params, paths)
		if !d.Bang && dd.seen[id] {
			return wrapREF(id, d.Mode, d.Target)
		}
		dd.seen[id] = true
		return wrapFull(id, d.Mode, d.Target, renderFileTree(paths, d.Params), d.Bang)
	case "GIT_FILE", "GIT_DIFF":
		return resolveGit(ctx, io, d, dd, maxDepth)
	default:
		return fmt.Sprintf("<!-- jixomd: unknown mode %q -->", d.Mode)
	}
}

// globPaths resolves a directive's target via IO.Glob, forwarding the relevant
// params (gitignore/ignore/ignoreFiles/dot) into GlobOptions. The target is
// first rebased to an absolute path per the jixomd path-resolution rules
// (document-relative by default; `pwd:` / `$PWD` escape to baseDir).
func globPaths(io fsio.IO, d *grammar.Directive, ro resolveOpts) ([]string, error) {
	opts := fsio.GlobOptions{
		Gitignore:   paramBool(d.Params, "gitignore", true),
		Ignore:      paramStrings(d.Params, "ignore"),
		IgnoreFiles: paramStrings(d.Params, "ignoreFiles"),
		Dot:         paramBool(d.Params, "dot", false),
	}
	target := rebaseTarget(d.Target, ro.baseDir, ro.docDir)
	return io.Glob(target, opts)
}

// pwdRe matches $PWD and ${PWD} only. Other $VAR names are intentionally NOT
// expanded here — core is pure (SPEC §6: no os import), so only the baseDir-
// bound $PWD is supported. General env expansion would belong in the CLI layer.
var pwdRe = regexp.MustCompile(`\$\{?PWD\}?\b`)

// rebaseTarget resolves a directive target to a path string ready for the
// backend's Glob, applying the jixomd path-resolution rules (SPEC §1.3.1). The
// steps, in order:
//
//  1. `$PWD` / `${PWD}` expansion → baseDir (NOT the process env, so a document
//     behaves identically regardless of the caller's shell). Other `$VAR` names
//     are left untouched — core is pure (SPEC §6: no os import); general env
//     expansion is the CLI's job if ever needed.
//  2. `pwd:` scheme: a leading `pwd:` prefix forces resolution against
//     baseDir (cwd / --base). The prefix is stripped and the remainder is joined
//     onto baseDir.
//  3. Document-relative default: anything still relative is joined onto
//     docDir (the source document's directory).
//
// Absolute paths pass through unchanged at each step. The joined result is NOT
// cleaned — leading `..` segments are preserved so the backend can pick a
// sensible walk root (the literal dir before the first `..`). When both baseDir
// and docDir are empty (e.g. the in-memory test harness), the target is returned
// verbatim so flat-keyed fake filesystemes keep working. Paths use forward
// slashes (package `path`); the local backend converts as needed.
func rebaseTarget(target, baseDir, docDir string) string {
	// 1. $PWD / ${PWD} expansion (pure — bound to baseDir, not the OS env).
	target = expandPWD(target, baseDir)

	// 2. pwd: scheme → baseDir-relative.
	if rest, ok := strings.CutPrefix(target, "pwd:"); ok {
		return joinBase(rest, baseDir)
	}

	// 3. Absolute passes through; otherwise document-relative.
	if path.IsAbs(target) {
		return target // not cleaned: keep '..' for walk-root heuristics
	}
	if baseDir == "" && docDir == "" {
		return target // pure/test harness: keep flat keys verbatim
	}
	return joinBase(target, docDir)
}

// expandPWD replaces $PWD / ${PWD} with baseDir. Other $VAR references are left
// untouched (core stays pure — no os.Getenv). SPEC §1.3.1.
func expandPWD(s, baseDir string) string {
	return pwdRe.ReplaceAllString(s, baseDir)
}

// joinBase joins rel onto base, treating rel as already-absolute when it is.
// Empty rel collapses to base. The result is NOT cleaned: leading `..` segments
// are preserved (manual join) so the backend can choose a walk root at the
// literal directory before the first `..` — cleaning would collapse
// docDir/../x → docDirParent/x and lose the anchoring dir. The backend
// normalizes both pattern and candidate before matching.
func joinBase(rel, base string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return base
	}
	if path.IsAbs(rel) {
		return rel
	}
	if base == "" {
		return rel
	}
	if strings.HasSuffix(base, "/") {
		return base + rel
	}
	return base + "/" + rel
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
		if d.Mode == "GIT_DIFF" {
			if compare := firstParam(d.Params, "compare"); compare != "" {
				left, right, err := parseGitCompare(compare)
				if err != nil {
					return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
				}
				return resolveGitRangeDiff(left, right, patterns, git, d.Params, d.Bang, dd)
			}
			if base := firstParam(d.Params, "base"); base != "" {
				return resolveGitBaseDiff(base, patterns, staged, git, d.Params, d.Bang, dd)
			}
		}

		// Working-tree view: list changed files matching the selected source.
		changed, err := git.ChangedFiles()
		if staged {
			changed, err = git.BaseChangedFiles("HEAD", true)
		}
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
		}
		matched := matchGitFiles(patterns, d.Params, changed)
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
		if gitPathIgnored(d.Params, p) {
			continue
		}
		f := fsio.GitFile{Path: p, Status: fsio.GitStatusModified}
		b.WriteString(renderGitFile(ctx, io, d.Mode, f, ref, staged, git, d.Params, d.Bang, dd, maxDepth))
	}
	if b.Len() == 0 {
		return noFoundBlock(d)
	}
	return b.String()
}

func resolveGitBaseDiff(base string, patterns []string, staged bool, git fsio.Git, params map[string][]string, bang bool, dd *dedup) string {
	changed, err := git.BaseChangedFiles(base, staged)
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
	}
	matched := matchGitFiles(patterns, params, changed)
	if len(matched) == 0 {
		return noFoundBlock(&grammar.Directive{Target: strings.Join(patterns, ","), Params: params})
	}
	source := fmt.Sprintf("base=%s;staged=%t", base, staged)
	var b strings.Builder
	for _, f := range matched {
		b.WriteString(renderGitDiffWith(gitDiffFunc(func(path string) (string, fsio.GitStatus, error) {
			return git.BaseDiff(base, path, staged)
		}), f, source, params, bang, dd))
	}
	return b.String()
}

func resolveGitRangeDiff(left, right string, patterns []string, git fsio.Git, params map[string][]string, bang bool, dd *dedup) string {
	changed, err := git.RangeChangedFiles(left, right)
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git error: %v -->", err)
	}
	matched := matchGitFiles(patterns, params, changed)
	if len(matched) == 0 {
		return noFoundBlock(&grammar.Directive{Target: strings.Join(patterns, ","), Params: params})
	}
	source := fmt.Sprintf("compare=%s..%s", left, right)
	var b strings.Builder
	for _, f := range matched {
		b.WriteString(renderGitDiffWith(gitDiffFunc(func(path string) (string, fsio.GitStatus, error) {
			return git.RangeDiff(left, right, path)
		}), f, source, params, bang, dd))
	}
	return b.String()
}

type gitDiffFunc func(path string) (string, fsio.GitStatus, error)

func renderGitDiffWith(diff gitDiffFunc, f fsio.GitFile, source string, params map[string][]string, bang bool, dd *dedup) string {
	id := stableID("GIT_DIFF" + "\x00" + f.Path + "\x00" + source)
	if bang {
		dd.seen[id] = true
	} else if dd.seen[id] {
		return wrapREF(id, "GIT_DIFF", f.Path)
	}
	dd.seen[id] = true

	content, status, err := diff(f.Path)
	if err != nil {
		return fmt.Sprintf("<!-- jixomd: git error %q: %v -->", f.Path, err)
	}
	if status == "" {
		status = f.Status
	}
	return wrapFull(id, "GIT_DIFF", f.Path, renderDiffContent(f.Path, status, content, params), bang)
}

func matchGitFiles(patterns []string, params map[string][]string, files []fsio.GitFile) []fsio.GitFile {
	var matched []fsio.GitFile
	for _, f := range files {
		if matchAny(patterns, f.Path) && !gitPathIgnored(params, f.Path) {
			matched = append(matched, f)
		}
	}
	return matched
}

func gitPathIgnored(params map[string][]string, file string) bool {
	for _, pattern := range paramStrings(params, "ignore") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(file, pattern) {
			return true
		}
		if !strings.Contains(pattern, "/") && globMatch(pattern, path.Base(file)) {
			return true
		}
		if matchAny([]string{pattern}, file) {
			return true
		}
	}
	return false
}

// renderGitFile renders one file's git content/diff with a status-suffixed title.
func renderGitFile(ctx context.Context, io fsio.IO, mode string, f fsio.GitFile, ref string, staged bool, git fsio.Git, params map[string][]string, bang bool, dd *dedup, maxDepth int) string {
	source := ref
	if source == "" {
		source = fmt.Sprintf("working;staged=%t", staged)
	}
	id := stableID(mode + "\x00" + f.Path + "\x00" + source)
	if bang {
		dd.seen[id] = true
	} else if dd.seen[id] {
		return wrapREF(id, mode, f.Path)
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
		processed = renderDiffContent(f.Path, status, content, params)
	}
	return wrapFull(id, mode, f.Path, processed, bang)
}

func renderDiffContent(path string, status fsio.GitStatus, content string, params map[string][]string) string {
	// Diff always uses diff fence regardless of extension.
	lang := firstParam(params, "lang")
	if lang == "" {
		lang = "diff"
	}
	fence := "```"
	if strings.Contains(content, fence) {
		fence = "````"
	}
	return fmt.Sprintf("`%s (%s)`\n\n%s%s\n%s\n%s\n", path, status, fence, lang, content, fence)
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

func parseGitCompare(compare string) (left, right string, err error) {
	if strings.Contains(compare, "...") {
		return "", "", fmt.Errorf("unsupported git compare %q: use left..right", compare)
	}
	left, right, ok := strings.Cut(compare, "..")
	if !ok || left == "" || right == "" || strings.Contains(right, "..") {
		return "", "", fmt.Errorf("malformed git compare %q: use left..right", compare)
	}
	return left, right, nil
}

// matchAny reports whether path matches any of the patterns. Pure (no
// doublestar dependency) so the core stays import-clean per SPEC §6. Handles
// the common glob surface: literal paths, **, dir/** prefixes, and per-segment
// * wildcards.
func matchAny(patterns []string, path string) bool {
	for _, pat := range patterns {
		if pat == "**" {
			return true
		}
		if globMatch(pat, path) {
			return true
		}
	}
	return false
}

// globMatch is a minimal pure glob matcher supporting:
//   - ** matches any number of path segments (including zero)
//   - * matches any chars except '/'
//   - ? matches a single non-'/' char
//   - everything else is literal
//
// It splits on '/' so ** is "any segments" and * is "within a segment".
func globMatch(pattern, path string) bool {
	return globSegs(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globSegs(pat, path []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// ** consumes zero or more path segments.
			if len(pat) == 1 {
				return true // trailing ** matches everything left
			}
			for i := 0; i <= len(path); i++ {
				if globSegs(pat[1:], path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		if !globSegment(pat[0], path[0]) {
			return false
		}
		pat, path = pat[1:], path[1:]
	}
	return len(path) == 0
}

// globSegment matches a single path segment against a pattern segment, where
// * matches any run of non-'/' chars and ? matches one. Implemented as a
// classic backtracking wildcard matcher.
func globSegment(pat, seg string) bool {
	pi, si := 0, 0
	starP, starS := -1, -1
	for si < len(seg) {
		if pi < len(pat) && (pat[pi] == '?' || pat[pi] == seg[si]) {
			pi++
			si++
		} else if pi < len(pat) && pat[pi] == '*' {
			starP = pi
			starS = si
			pi++
		} else if starP >= 0 {
			pi = starP + 1
			starS++
			si = starS
		} else {
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
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
func renderFile(ctx context.Context, io fsio.IO, mode, path string, params map[string][]string, bang bool, dd *dedup, maxDepth int, ro resolveOpts) string {
	id := stableID(mode + "\x00" + path)

	// `!` forces full content in an isolated dedup scope (SPEC §3.3). Its nested
	// directives resolve in a fresh scope, but the block's own id IS registered
	// in the parent scope so a later ordinary [path](@FILE) can REF it.
	if bang {
		nestedDD := newDedup()
		content, _ := io.ReadFile(path)
		processed := applyOutputShaping(mode, path, string(content), params)
		if mode == "INJECT" {
			processed = expandInjected(ctx, io, processed, nestedDD, maxDepth, ro)
		}
		dd.seen[id] = true
		return wrapFull(id, mode, path, processed, true)
	}

	// Repeat (no `!`): emit REF, do not recurse. Terminates cycles.
	if dd.seen[id] {
		return wrapREF(id, mode, path)
	}
	dd.seen[id] = true

	content, rerr := io.ReadFile(path)
	if rerr != nil {
		return fmt.Sprintf("<!-- jixomd: read error %q: %v -->", path, rerr)
	}
	processed := applyOutputShaping(mode, path, string(content), params)
	// INJECT content may itself contain directives → expand them in the same
	// dedup scope so cross-file cycles terminate.
	if mode == "INJECT" {
		processed = expandInjected(ctx, io, processed, dd, maxDepth, ro)
	}
	return wrapFull(id, mode, path, processed, false)
}

// expandInjected recursively expands directives inside injected content,
// sharing the parent dedup scope (so cycles terminate). Bounded by maxDepth.
// Nested directives inherit the same path-resolution context (ro) as the
// surrounding document.
func expandInjected(ctx context.Context, io fsio.IO, content string, dd *dedup, maxDepth int, ro resolveOpts) string {
	cur := content
	for i := 0; i < maxDepth; i++ {
		if err := ctx.Err(); err != nil {
			return cur
		}
		out, found, err := expandOnce(ctx, io, cur, dd, maxDepth, ro)
		if err != nil || !found {
			return out
		}
		cur = out
	}
	return cur
}

// wrapFull emits the START/content/END form. SPEC §3.1. The START marker
// includes the directive mode (e.g. FILE, INJECT) so the AI can see which
// instruction produced this block.
func wrapFull(id, mode, path, content string, bang bool) string {
	startLine := fmt.Sprintf("<!-- jixomd:START id=%s mode=%s path=%q -->", id, mode, path)
	if bang {
		startLine = fmt.Sprintf("<!-- jixomd:START id=%s mode=%s path=%q force=1 -->", id, mode, path)
	}
	return startLine + "\n" + content + "\n<!-- jixomd:END id=" + id + " -->\n"
}

// wrapREF emits the self-closing START / REF / END form for a repeat. SPEC §3.1.
func wrapREF(id, mode, path string) string {
	return fmt.Sprintf("<!-- jixomd:START id=%s mode=%s path=%q /-->\n<!-- jixomd:REF -->\n<!-- jixomd:END id=%s -->\n", id, mode, path, id)
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

// listTreeID computes a strong content id for FILE_LIST / FILE_TREE so dedup is
// correct (issue 003): the id incorporates the mode, target, normalized params,
// and the resolved file set. Two directives dedup iff they produce the same
// output; differing params (dot, prefix, …) or file sets keep distinct ids.
func listTreeID(mode, target string, params map[string][]string, paths []string) string {
	var b strings.Builder
	b.WriteString(mode)
	b.WriteByte('\x00')
	b.WriteString(target)
	b.WriteByte('\x00')
	// Normalize params deterministically.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strings.Join(params[k], ","))
		b.WriteByte(';')
	}
	b.WriteByte('\x00')
	b.WriteString(strings.Join(paths, "\n"))
	return stableID(b.String())
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
