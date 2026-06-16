// Package local is the OS-backed fsio.IO implementation used by the jixomd CLI.
// It is the only place that touches the real filesystem / git; the pure core
// never imports this. See SPEC §2 / §6.
package local

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	gitignore "github.com/sabhiram/go-gitignore"
	fsio "github.com/jixoai/jixomd/io"
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

	var matches []string
	_ = filepath.WalkDir(l.Base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(l.Base, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
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
		if !opts.Dot && len(rel) > 0 && rel[0] == '.' {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if ok, _ := doublestar.Match(pattern, rel); ok {
			matches = append(matches, rel)
		}
		return nil
	})
	sort.Strings(matches)
	return matches, nil
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
