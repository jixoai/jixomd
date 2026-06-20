// Package local is the OS-backed fsio.IO implementation used by the jixomd CLI.
// It is the only place that touches the real filesystem / git; the pure core
// never imports this. See SPEC §2 / §6.
package local

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	fsio "github.com/jixoai/jixomd/io"
	gitignore "github.com/sabhiram/go-gitignore"
)

// Local implements fsio.IO over a base directory.
type Local struct {
	Base string
}

// New returns an OS-backed IO rooted at base.
func New(base string) *Local {
	if base == "" {
		base, _ = os.Getwd()
	}
	return &Local{Base: base}
}

// Stat reports whether path is a file or directory under Base.
func (l *Local) Stat(path string) (fsio.Entry, error) {
	full := l.abs(path)
	info, err := os.Stat(full)
	if err != nil {
		return fsio.Entry{}, err
	}
	return fsio.Entry{Path: path, IsDir: info.IsDir()}, nil
}

// ReadFile reads a file under Base as bytes.
func (l *Local) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(l.abs(path))
}

// Glob matches a pattern (with '**') under Base, honoring gitignore and the
// Ignore / IgnoreFiles options (issue 004).
//
// The pattern may be relative (resolved against Base) or absolute. Core rebases
// document-relative / `pwd:` / `$PWD` targets to absolute before calling, so an
// absolute pattern can reach files outside Base (e.g. `[../*.md](@FILE)`). In
// that case matching is done against each file's absolute path, but reported
// paths stay relative to Base (falling back to the absolute path only when the
// file is not under Base) so emitted blocks stay readable and deterministic.
func (l *Local) Glob(pattern string, opts fsio.GlobOptions) ([]string, error) {
	var ignores []*gitignore.GitIgnore
	if opts.Gitignore {
		if gi, err := gitignore.CompileIgnoreFile(filepath.Join(l.Base, ".gitignore")); err == nil {
			ignores = append(ignores, gi)
		}
	}
	// Custom ignore files (issue 004).
	for _, f := range opts.IgnoreFiles {
		if gi, err := gitignore.CompileIgnoreFile(filepath.Join(l.Base, f)); err == nil {
			ignores = append(ignores, gi)
		}
	}
	// Inline ignore patterns, compiled against Base (issue 004).
	if len(opts.Ignore) > 0 {
		if gi := gitignore.CompileIgnoreLines(opts.Ignore...); gi != nil {
			ignores = append(ignores, gi)
		}
	}

	// Absolute patterns can reach outside Base. Walk from the deepest existing
	// ancestor of the pattern's directory to avoid scanning the whole world.
	// The pattern is cleaned (collapsing `..`) so matching is well-defined; the
	// walk root is the cleaned pattern's literal directory prefix, and each
	// candidate is compared against the cleaned pattern.
	walkRoot := l.Base
	patAbs := filepath.IsAbs(pattern)
	cleanPattern := pattern
	if patAbs {
		cleanPattern = filepath.Clean(pattern)
		walkRoot = globRoot(cleanPattern)
		if walkRoot == "" || !pathExists(walkRoot) {
			walkRoot = l.Base
		}
	}

	var matches []string
	_ = filepath.WalkDir(walkRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(l.Base, p)
		rel = filepath.ToSlash(rel)
		// rel may start with ".." for files outside Base; that's fine for
		// ignore/match but skip the walk-root's own "." entry only when it is
		// literally Base.
		if p == l.Base {
			return nil
		}
		for _, gi := range ignores {
			if gi.MatchesPath(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		// Dot filter: hide dotfiles/dotdirs unless opts.Dot. Note ".." / "." are
		// navigation entries, NOT hidden files, so they must not trigger this —
		// otherwise walking a root outside Base (rel starts with "..") would
		// SkipDir the root itself.
		base := filepath.Base(rel)
		if !opts.Dot && base != "." && base != ".." && len(base) > 0 && base[0] == '.' {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Match: relative patterns compare against rel-to-Base; absolute
		// patterns compare against the cleaned absolute path of the candidate.
		candidate := rel
		if patAbs {
			candidate = filepath.Clean(filepath.ToSlash(p))
		}
		if ok, _ := doublestar.Match(cleanPattern, candidate); ok {
			matches = append(matches, rel)
		}
		return nil
	})
	sort.Strings(matches)
	return matches, nil
}

// pathExists reports whether p exists on disk (file or dir).
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// globRoot returns the longest literal directory prefix of a glob pattern —
// the deepest existing ancestor to start walking from. For an absolute pattern
// like /a/b/**/c.go it returns /a/b. Returns "" if no clean prefix exists.
func globRoot(pattern string) string {
	pattern = filepath.ToSlash(pattern)
	// Strip everything from the first glob metacharacter onward.
	cut := len(pattern)
	for i, r := range pattern {
		if r == '*' || r == '?' || r == '[' || r == '{' {
			cut = i
			break
		}
	}
	dir := pattern[:cut]
	if idx := strings.LastIndexByte(dir, '/'); idx >= 0 {
		dir = dir[:idx]
	}
	return filepath.Clean(dir)
}

// Git returns a go-git-backed Git capability. If Base is not inside a git
// repository, returns ErrUnsupported so @GIT_* degrades to a comment.
// Implemented in git.go.
func (l *Local) Git() (fsio.Git, error) { return l.gitBackend() }

// HTTP declines the http capability in the scaffold. TODO: net/http backend.
func (l *Local) HTTP() (fsio.HTTP, error) { return nil, fsio.ErrUnsupported }

func (l *Local) abs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(l.Base, path)
}
