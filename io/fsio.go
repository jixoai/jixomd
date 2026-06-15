// Package fsio defines the IO capability boundary for jixomd's core.
//
// The pure core (package core) depends only on this abstraction, never on the
// concrete backend. This is the "wasm host-import" seam: a host implements IO,
// the core consumes it. See SPEC §2.2 / §6.
package fsio

import "errors"

// ErrUnsupported signals that an optional capability (Git / HTTP) is not
// available in the current backend. The core degrades the corresponding
// directives to a comment instead of failing.
var ErrUnsupported = errors.New("jixomd: capability unsupported")

// Entry is the result of Stat.
type Entry struct {
	Path  string
	IsDir bool
}

// GlobOptions controls Glob behavior. Full gitignore semantics live in the
// backend; the core only forwards options. See SPEC §1.4 (subset).
type GlobOptions struct {
	Gitignore   bool
	Ignore      []string
	IgnoreFiles []string
	Dot         bool
}

// IO is the capability interface the core consumes. Filesystem ops are
// required; Git and HTTP are optional (return ErrUnsupported to decline).
type IO interface {
	Stat(path string) (Entry, error)
	ReadFile(path string) ([]byte, error)
	Glob(pattern string, opts GlobOptions) ([]string, error)
	Git() (Git, error)
	HTTP() (HTTP, error)
}

// GitStatus is a one-letter git file change status (A/M/D/R/T/U).
type GitStatus string

const (
	GitStatusAdded    GitStatus = "A"
	GitStatusModified GitStatus = "M"
	GitStatusDeleted  GitStatus = "D"
	GitStatusRenamed  GitStatus = "R"
	GitStatusType     GitStatus = "T"
	GitStatusUnmerged GitStatus = "U"
)

// GitFile pairs a path with its change status.
type GitFile struct {
	Path   string
	Status GitStatus
}

// Git is the optional git capability. See SPEC §2.2.
type Git interface {
	ChangedFiles() ([]GitFile, error)
	WorkingContent(path string, staged bool) (string, GitStatus, error)
	WorkingDiff(path string, staged bool) (string, GitStatus, error)
	FilesAtCommit(ref string, globs []string) ([]string, error)
	CommitContent(ref, path string) (string, GitStatus, error)
	CommitDiff(ref, path string) (string, GitStatus, error)
}

// HTTP is the optional network capability (for URL targets).
type HTTP interface {
	Get(url string) (body []byte, contentType string, err error)
}
