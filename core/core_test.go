package core_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	fsio "github.com/jixoai/jixomd/io"
	"github.com/jixoai/jixomd/core"
)

// fakeIO is an in-memory fsio.IO. It proves the core is pure: tests run with no
// filesystem, no git, no network — only the injected capability.
type fakeIO struct{ files map[string]string }

func (f *fakeIO) Stat(p string) (fsio.Entry, error) {
	if _, ok := f.files[p]; ok {
		return fsio.Entry{Path: p}, nil
	}
	return fsio.Entry{}, os.ErrNotExist
}
func (f *fakeIO) ReadFile(p string) ([]byte, error) {
	if c, ok := f.files[p]; ok {
		return []byte(c), nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeIO) Glob(pattern string, _ fsio.GlobOptions) ([]string, error) {
	var out []string
	for k := range f.files {
		if ok, _ := doublestar.Match(pattern, k); ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (f *fakeIO) Git() (fsio.Git, error)   { return nil, fsio.ErrUnsupported }
func (f *fakeIO) HTTP() (fsio.HTTP, error) { return nil, fsio.ErrUnsupported }

func TestExpand_ExemptsCommentsCodeAndPreservesProse(t *testing.T) {
	io := &fakeIO{files: map[string]string{
		"a.txt": "hello world",
		"b.txt": "SECRET-B",
		"c.txt": "SECRET-C",
	}}
	doc := "# Title\n\n" +
		"Real file: [a.txt](@FILE)\n\n" +
		"<!-- [b.txt](@FILE) should NOT expand -->\n\n" +
		"Inline code: `[c.txt](@FILE)` stays literal too.\n\n" +
		"```\n[c.txt](@FILE) inside fenced block\n```\n\n" +
		"Inject: [a.txt](@INJECT)\n\n" +
		"Normal link [google](https://example.com) untouched.\n"

	out, err := core.Expand(context.Background(), io, doc, core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// a.txt content appears (via FILE and INJECT).
	if strings.Count(out, "hello world") != 2 {
		t.Errorf("want a.txt content twice (FILE + INJECT), got %d times\n%s", strings.Count(out, "hello world"), out)
	}
	// START/END markers present.
	if !strings.Contains(out, "<!-- jixomd:START id=") || !strings.Contains(out, "<!-- jixomd:END id=") {
		t.Errorf("missing START/END markers\n%s", out)
	}
	// Directives in comment / code are NOT expanded (their file content must not leak).
	if strings.Contains(out, "SECRET-B") {
		t.Errorf("b.txt leaked from HTML comment\n%s", out)
	}
	if strings.Contains(out, "SECRET-C") {
		t.Errorf("c.txt leaked from code span/block\n%s", out)
	}
	// The literal directive syntax inside comment / code is preserved verbatim.
	if !strings.Contains(out, "<!-- [b.txt](@FILE) should NOT expand -->") {
		t.Errorf("HTML comment was altered\n%s", out)
	}
	if !strings.Contains(out, "`[c.txt](@FILE)` stays literal too.") {
		t.Errorf("inline code directive was altered\n%s", out)
	}
	if !strings.Contains(out, "[c.txt](@FILE) inside fenced block") {
		t.Errorf("fenced code directive was altered\n%s", out)
	}
	// A normal link is left untouched.
	if !strings.Contains(out, "[google](https://example.com) untouched.") {
		t.Errorf("normal link was altered\n%s", out)
	}
}

func TestExpand_StripsFrontmatter(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "body"}}
	doc := "---\ntitle: x\ncwd: /tmp\n---\n\n[a.txt](@INJECT)\n"
	out, err := core.Expand(context.Background(), io, doc, core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if strings.Contains(out, "title: x") || strings.Contains(out, "cwd: /tmp") {
		t.Errorf("frontmatter not stripped\n%s", out)
	}
	if !strings.Contains(out, "body") {
		t.Errorf("body lost\n%s", out)
	}
}

func TestExpand_NoFilesEmitsComment(t *testing.T) {
	io := &fakeIO{files: map[string]string{}}
	out, err := core.Expand(context.Background(), io, "[missing/**](@FILE)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "no files for") {
		t.Errorf("expected no-files comment, got\n%s", out)
	}
}

func TestExpand_FILE_LIST(t *testing.T) {
	io := &fakeIO{files: map[string]string{
		"src/a.go":  "A",
		"src/b.go":  "B",
		"README.md": "C",
	}}
	out, err := core.Expand(context.Background(), io, "[src/**](@FILE_LIST)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "src/a.go") || !strings.Contains(out, "src/b.go") {
		t.Errorf("FILE_LIST missing paths\n%s", out)
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("FILE_LIST should only match src/**\n%s", out)
	}
}

func TestExpand_FILE_TREE(t *testing.T) {
	io := &fakeIO{files: map[string]string{
		"src/a.go":     "A",
		"src/sub/b.go": "B",
	}}
	out, err := core.Expand(context.Background(), io, "[src/**](@FILE_TREE)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "├──") && !strings.Contains(out, "└──") {
		t.Errorf("FILE_TREE missing connectors\n%s", out)
	}
	if !strings.Contains(out, "src") {
		t.Errorf("FILE_TREE missing directory name\n%s", out)
	}
}

func TestExpand_LangParam(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "hello"}}
	out, err := core.Expand(context.Background(), io, "[a.txt](@FILE?lang=python)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "```python") {
		t.Errorf("lang=python not applied\n%s", out)
	}
}

func TestExpand_PrefixParam(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "line1"}}
	out, err := core.Expand(context.Background(), io, "[a.txt](@INJECT?prefix=> )\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "> line1") {
		t.Errorf("prefix not applied\n%s", out)
	}
}

func TestExpand_NoFoundMsgParam(t *testing.T) {
	io := &fakeIO{files: map[string]string{}}
	out, err := core.Expand(context.Background(), io, "[missing/**](@FILE?noFound.msg=CUSTOM)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "CUSTOM") {
		t.Errorf("noFound.msg not applied\n%s", out)
	}
}

func TestExpand_GitUnsupportedDegrades(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "x"}}
	out, err := core.Expand(context.Background(), io, "[a.txt](@GIT_FILE)\n", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(out, "git unsupported") {
		t.Errorf("expected git unsupported comment\n%s", out)
	}
}
