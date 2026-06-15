// Package grammar defines the jixomd directive grammar and its goldmark inline
// parser.
//
// A directive is syntactically a standard Markdown link whose destination
// starts with '@', e.g. `[src/**](@FILE)`. The parser recognizes it as a custom
// AST node (Directive) carrying its exact source span, so the core can splice
// resolved content back byte-for-byte while leaving directives that appear
// inside code spans, inline code, or HTML comments completely untouched — those
// are never parsed as links by CommonMark, so no Directive node is ever created
// for them. This is the correctness fix called out in SPEC §1.1.
package grammar

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// KindDirective is the AST node kind for a parsed directive.
var KindDirective = ast.NewNodeKind("Directive")

// Directive is a parsed [target](@MODE!?params) AST node.
type Directive struct {
	ast.BaseInline
	Segment text.Segment          // source span of the whole [..](@..)
	Target  string                // link text (backtick-wrapping stripped)
	Dest    string                // raw '@...' destination
	Mode    string                // normalized: upper case, '-' -> '_'
	Bang    bool                  // trailing '!' (force-skip-cache, SPEC §3)
	Params  map[string][]string   // query params
}

// Kind reports the node kind.
func (d *Directive) Kind() ast.NodeKind { return KindDirective }

// Dump implements ast.Node.
func (d *Directive) Dump(source []byte, level int) {
	ast.DumpHelper(d, source, level, map[string]string{
		"target": d.Target,
		"dest":   d.Dest,
		"mode":   d.Mode,
	}, nil)
}

var destRe = regexp.MustCompile(`^@([A-Za-z][A-Za-z0-9_-]*)(!)?(\?.*)?$`)

// ParseDest splits a link destination '@...' into mode/bang/params.
// Returns ok=false when it is not a jixomd directive destination.
func ParseDest(dest string) (mode string, bang bool, params map[string][]string, ok bool) {
	m := destRe.FindStringSubmatch(dest)
	if m == nil {
		return "", false, nil, false
	}
	mode = strings.ToUpper(strings.ReplaceAll(m[1], "-", "_"))
	bang = m[2] == "!"
	if m[3] != "" {
		if q, err := url.ParseQuery(strings.TrimPrefix(m[3], "?")); err == nil {
			params = map[string][]string(q)
		}
	}
	return mode, bang, params, true
}

// directiveParser is a goldmark inline parser that turns [..](@..) into a
// Directive node. Registered above the built-in link parser (priority 999 vs
// 200) so it wins on directive links; returns nil for ordinary '[' so normal
// links and images are still handled by the built-in parser.
type directiveParser struct{}

// NewDirectiveParser returns the directive inline parser.
func NewDirectiveParser() parser.InlineParser { return &directiveParser{} }

// Trigger fires on the opening bracket.
func (p *directiveParser) Trigger() []byte { return []byte{'['} }

// Parse attempts to consume a single-line [target](@dest) directive.
func (p *directiveParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine() // line[0] == '['; seg.Start is its source offset
	d, consumed, ok := scanDirective(line)
	if !ok {
		return nil // defer to the built-in link parser (reader not advanced)
	}
	d.Segment = text.NewSegment(seg.Start, seg.Start+consumed)
	block.Advance(consumed)
	return d
}

// scanDirective parses line (starting at '[') as [target](@dest).
// Limitation (scaffold): no nested/escaped brackets inside the target. The
// closing ']' and ')' are taken as the first occurrence; directives are
// single-line. TODO: handle escaped chars per CommonMark.
func scanDirective(line []byte) (d *Directive, consumed int, ok bool) {
	relClose := bytes.IndexByte(line[1:], ']')
	if relClose < 0 {
		return nil, 0, false
	}
	closeIdx := relClose + 1 // index of ']' within line
	if closeIdx+1 >= len(line) || line[closeIdx+1] != '(' {
		return nil, 0, false
	}
	relParen := bytes.IndexByte(line[closeIdx+2:], ')')
	if relParen < 0 {
		return nil, 0, false
	}
	parenIdx := relParen + closeIdx + 2 // index of ')' within line

	target := string(line[1:closeIdx])
	target = strings.Trim(target, "`") // strip backtick-wrapping, e.g. [`a/b/c`]

	dest := string(line[closeIdx+2 : parenIdx])
	if !strings.HasPrefix(dest, "@") {
		return nil, 0, false
	}
	mode, bang, params, destOK := ParseDest(dest)
	if !destOK {
		return nil, 0, false
	}
	d = &Directive{
		Target: target,
		Dest:   dest,
		Mode:   mode,
		Bang:   bang,
		Params: params,
	}
	return d, parenIdx + 1, true
}
