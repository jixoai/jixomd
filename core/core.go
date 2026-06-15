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
		paths, err := io.Glob(d.Target, fsio.GlobOptions{Gitignore: true})
		if err != nil {
			return fmt.Sprintf("<!-- jixomd: glob error %q: %v -->", d.Target, err)
		}
		if len(paths) == 0 {
			return fmt.Sprintf("<!-- jixomd: no files for %q -->", d.Target)
		}
		sort.Strings(paths)
		var b strings.Builder
		for _, p := range paths {
			b.WriteString(renderFile(ctx, io, d.Mode, p, d.Params, d.Bang, dd, maxDepth))
		}
		return b.String()
	case "FILE_TREE", "FILE_LIST", "GIT_FILE", "GIT_DIFF":
		return fmt.Sprintf("<!-- jixomd: mode %s not implemented in scaffold (TODO) -->", d.Mode)
	default:
		return fmt.Sprintf("<!-- jixomd: unknown mode %q -->", d.Mode)
	}
}

// renderFile produces the START/END-wrapped block for one file, applying dedup.
func renderFile(ctx context.Context, io fsio.IO, mode, path string, params map[string][]string, bang bool, dd *dedup, maxDepth int) string {
	id := stableID(mode + "\x00" + path)

	// `!` forces full content in an isolated dedup scope (SPEC §3.3).
	if bang {
		nestedDD := newDedup()
		content, _ := io.ReadFile(path)
		processed := applyOutputShaping(mode, path, string(content), params)
		if mode == "INJECT" {
			processed = expandInjected(ctx, io, processed, nestedDD, maxDepth)
		}
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
// lang/prefix params. Pure.
func applyOutputShaping(mode, path, content string, params map[string][]string) string {
	if mode != "FILE" {
		return content
	}
	lang := firstParam(params, "lang")
	if lang == "" {
		lang = extOf(path)
	}
	fence := "```"
	if strings.Contains(content, fence) {
		fence = "````"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "`%s`\n\n", path)
	fmt.Fprintf(&b, "%s%s\n%s\n%s\n", fence, lang, content, fence)
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
