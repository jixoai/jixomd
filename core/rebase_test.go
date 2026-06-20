package core_test

import (
	"context"
	"testing"

	"github.com/jixoai/jixomd/core"
	fsio "github.com/jixoai/jixomd/io"
)

// rebaseProbe is a fakeIO that records the exact target string handed to Glob,
// so we can assert the rebase logic without depending on the local backend.
type rebaseProbe struct {
	files map[string]string
	got   string
}

func (p *rebaseProbe) Stat(path string) (fsio.Entry, error) { return fsio.Entry{Path: path}, nil }
func (p *rebaseProbe) ReadFile(path string) ([]byte, error) { return []byte(p.files[path]), nil }
func (p *rebaseProbe) Glob(pattern string, _ fsio.GlobOptions) ([]string, error) {
	p.got = pattern
	return nil, nil
}
func (p *rebaseProbe) Git() (fsio.Git, error)   { return nil, fsio.ErrUnsupported }
func (p *rebaseProbe) HTTP() (fsio.HTTP, error) { return nil, fsio.ErrUnsupported }

// TestExpand_RebaseTargetRules asserts the target string that core forwards to
// Glob under each path-resolution rule, with a fixed base/docDir.
func TestExpand_RebaseTargetRules(t *testing.T) {
	cases := []struct {
		name      string
		directive string
		want      string
	}{
		{"relative is doc-relative", "[x.txt](@FILE)", "/DOC/x.txt"},
		{"dotdot is doc-relative (uncleaned)", "[../y.txt](@FILE)", "/DOC/../y.txt"},
		{"glob suffix stays doc-relative", "[../z/*.md](@FILE)", "/DOC/../z/*.md"},
		{"pwd scheme is base-relative", "[pwd:z.txt](@FILE)", "/BASE/z.txt"},
		{"$PWD is base-relative", "[$PWD/w.txt](@FILE)", "/BASE/w.txt"},
		{"absolute passes through", "[/abs/a.txt](@FILE)", "/abs/a.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &rebaseProbe{}
			_, err := core.Expand(context.Background(), p, c.directive, core.Options{
				BaseDir: "/BASE",
				DocDir:  "/DOC",
			})
			if err != nil {
				t.Fatalf("Expand: %v", err)
			}
			if p.got != c.want {
				t.Errorf("target forwarded to Glob:\n got %q\nwant %q", p.got, c.want)
			}
		})
	}
}

// TestExpand_RebasePassesThroughWhenNoDirs covers the in-memory test harness
// path: when both BaseDir and DocDir are empty, targets pass through verbatim
// so flat-keyed fake filesystems keep working.
func TestExpand_RebasePassesThroughWhenNoDirs(t *testing.T) {
	p := &rebaseProbe{files: map[string]string{"a/b.txt": "X"}}
	// Existing core_test fakeIO would receive the raw target unchanged.
	_, err := core.Expand(context.Background(), p, "[a/b.txt](@FILE)", core.Options{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if p.got != "a/b.txt" {
		t.Errorf("want verbatim %q, got %q", "a/b.txt", p.got)
	}
}

// TestExpand_RebasePwdWithDotdot confirms `pwd:` and `..` compose: the scheme
// is stripped, then the remainder is joined onto baseDir (uncleaned).
func TestExpand_RebasePwdWithDotdot(t *testing.T) {
	p := &rebaseProbe{}
	_, _ = core.Expand(context.Background(), p, "[pwd:../x.txt](@FILE)", core.Options{
		BaseDir: "/BASE",
		DocDir:  "/DOC",
	})
	if p.got != "/BASE/../x.txt" {
		t.Errorf("want /BASE/../x.txt, got %q", p.got)
	}
}

// TestExpand_RebasePWDDoesNotOvermatch ensures $PWD doesn't match $PWDX or
// similar prefixes (word boundary).
func TestExpand_RebasePWDDoesNotOvermatch(t *testing.T) {
	p := &rebaseProbe{}
	_, _ = core.Expand(context.Background(), p, "[$PWDX/y](@FILE)", core.Options{
		BaseDir: "/BASE",
		DocDir:  "/DOC",
	})
	// $PWDX is NOT $PWD → left untouched → relative → doc-relative.
	if p.got != "/DOC/$PWDX/y" {
		t.Errorf("want /DOC/$PWDX/y (no overmatch), got %q", p.got)
	}
}
