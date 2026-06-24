package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	fsio "github.com/jixoai/jixomd/io"
)

// localGit implements fsio.Git over a go-git repository rooted at base.
type localGit struct {
	base string
	repo *git.Repository
}

// gitBackend is the Git() entry for *Local, defined here to keep the go-git
// dependency isolated from local.go.
func (l *Local) gitBackend() (fsio.Git, error) {
	repo, err := git.PlainOpenWithOptions(l.Base, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return nil, fsio.ErrUnsupported
		}
		return nil, err
	}
	return &localGit{base: l.Base, repo: repo}, nil
}

// ChangedFiles returns the worktree's changed files (working + staged) with
// one-letter statuses, relative to HEAD.
func (g *localGit) ChangedFiles() ([]fsio.GitFile, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, err
	}
	status, err := wt.Status()
	if err != nil {
		return nil, err
	}
	var out []fsio.GitFile
	for path, s := range status {
		st := classifyStatus(s)
		if st == "" {
			continue
		}
		out = append(out, fsio.GitFile{Path: path, Status: st})
	}
	return out, nil
}

// classifyStatus maps a go-git FileStatus (staging + worktree codes) to our
// one-letter status.
func classifyStatus(s *git.FileStatus) fsio.GitStatus {
	if s == nil {
		return ""
	}
	switch {
	case s.Staging == git.Modified || s.Worktree == git.Modified:
		return fsio.GitStatusModified
	case s.Staging == git.Added || s.Worktree == git.Added:
		return fsio.GitStatusAdded
	case s.Staging == git.Deleted || s.Worktree == git.Deleted:
		return fsio.GitStatusDeleted
	case s.Staging == git.Renamed || s.Worktree == git.Renamed:
		return fsio.GitStatusRenamed
	case s.Staging == git.Untracked:
		return fsio.GitStatusAdded
	default:
		return fsio.GitStatusModified
	}
}

// BaseChangedFiles returns files changed between base and the current
// worktree/index snapshot.
func (g *localGit) BaseChangedFiles(base string, staged bool) ([]fsio.GitFile, error) {
	baseContents, err := g.refContents(base)
	if err != nil {
		return nil, err
	}
	currentContents, err := g.currentContents(staged)
	if err != nil {
		return nil, err
	}
	return diffContentMaps(baseContents, currentContents), nil
}

// BaseDiff returns a unified diff for one file between base and the current
// worktree/index snapshot.
func (g *localGit) BaseDiff(base, path string, staged bool) (string, fsio.GitStatus, error) {
	baseContent, baseExists, err := g.refContent(base, path)
	if err != nil {
		return "", "", err
	}
	currentContent, currentExists, err := g.currentContent(path, staged)
	if err != nil {
		return "", "", err
	}
	status := statusFromExistence(baseExists, currentExists)
	return unifiedDiff(path, baseContent, currentContent), status, nil
}

// RangeChangedFiles returns files changed from left to right.
func (g *localGit) RangeChangedFiles(left, right string) ([]fsio.GitFile, error) {
	leftContents, err := g.refContents(left)
	if err != nil {
		return nil, err
	}
	rightContents, err := g.refContents(right)
	if err != nil {
		return nil, err
	}
	return diffContentMaps(leftContents, rightContents), nil
}

// RangeDiff returns a unified diff for one file from left to right.
func (g *localGit) RangeDiff(left, right, path string) (string, fsio.GitStatus, error) {
	leftContent, leftExists, err := g.refContent(left, path)
	if err != nil {
		return "", "", err
	}
	rightContent, rightExists, err := g.refContent(right, path)
	if err != nil {
		return "", "", err
	}
	status := statusFromExistence(leftExists, rightExists)
	return unifiedDiff(path, leftContent, rightContent), status, nil
}

// WorkingContent returns the working-tree content of path (staged selects the
// index version). Status is the file's change status.
func (g *localGit) WorkingContent(path string, staged bool) (string, fsio.GitStatus, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return "", "", err
	}
	status, err := wt.Status()
	if err != nil {
		return "", "", err
	}
	st := classifyStatus(status[path])
	if staged {
		// Read from the index.
		idx, err := g.repo.Storer.Index()
		if err != nil {
			return "", st, err
		}
		for _, e := range idx.Entries {
			if e.Name == path {
				blob, err := g.repo.BlobObject(e.Hash)
				if err != nil {
					return "", st, err
				}
				r, err := blob.Reader()
				if err != nil {
					return "", st, err
				}
				defer r.Close()
				buf := new(strings.Builder)
				if _, err := readAll(buf, r); err != nil {
					return "", st, err
				}
				return buf.String(), st, nil
			}
		}
		if st == fsio.GitStatusDeleted {
			return "", st, nil
		}
		return "", st, fmt.Errorf("not in index: %s", path)
	}
	// Working-tree file on disk.
	b, err := readFileBytes(filepath.Join(g.base, path))
	if err != nil {
		if os.IsNotExist(err) && st == fsio.GitStatusDeleted {
			return "", st, nil
		}
		return "", st, err
	}
	return string(b), st, nil
}

// WorkingDiff returns the diff of path vs HEAD (staged selects the staged
// version vs HEAD). Uses a unified diff between the HEAD blob and the
// working/index content.
func (g *localGit) WorkingDiff(path string, staged bool) (string, fsio.GitStatus, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return "", "", err
	}
	status, err := wt.Status()
	if err != nil {
		return "", "", err
	}
	st := classifyStatus(status[path])

	headContent, headExists, err := g.refContent("HEAD", path)
	if err != nil {
		headContent = ""
		headExists = false
	}

	curContent, curExists, err := g.currentContent(path, staged)
	if err != nil {
		return "", st, err
	}
	if st == "" {
		st = statusFromExistence(headExists, curExists)
	}

	return unifiedDiff(path, headContent, curContent), st, nil
}

// FilesAtCommit lists files at ref matching the given glob patterns.
func (g *localGit) FilesAtCommit(ref string, globs []string) ([]string, error) {
	hash, err := g.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	commit, err := g.repo.CommitObject(hash)
	if err != nil {
		return nil, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	var out []string
	_ = tree.Files().ForEach(func(f *object.File) error {
		if matchGlobs(globs, f.Name) {
			out = append(out, f.Name)
		}
		return nil
	})
	return out, nil
}

// CommitContent returns the content of path at ref (git show ref:path).
func (g *localGit) CommitContent(ref, path string) (string, fsio.GitStatus, error) {
	hash, err := g.resolveRef(ref)
	if err != nil {
		return "", "", err
	}
	commit, err := g.repo.CommitObject(hash)
	if err != nil {
		return "", "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", "", err
	}
	file, err := tree.File(path)
	if err != nil {
		return "", "", err
	}
	contents, err := file.Contents()
	if err != nil {
		return "", "", err
	}
	return contents, fsio.GitStatusModified, nil
}

// CommitDiff returns the diff introduced by commit `ref` for path (vs parent).
func (g *localGit) CommitDiff(ref, path string) (string, fsio.GitStatus, error) {
	hash, err := g.resolveRef(ref)
	if err != nil {
		return "", "", err
	}
	commit, err := g.repo.CommitObject(hash)
	if err != nil {
		return "", "", err
	}
	// Parent version of the file.
	var parentContent string
	if commit.NumParents() > 0 {
		if p, perr := commit.Parent(0); perr == nil {
			if tree, terr := p.Tree(); terr == nil {
				if f, ferr := tree.File(path); ferr == nil {
					if c, ferr := f.Contents(); ferr == nil {
						parentContent = c
					}
				}
			}
		}
	}
	// Commit version of the file.
	commitTree, err := commit.Tree()
	if err != nil {
		return "", "", err
	}
	var commitContent string
	if f, ferr := commitTree.File(path); ferr == nil {
		if c, ferr := f.Contents(); ferr == nil {
			commitContent = c
		}
	}
	return unifiedDiff(path, parentContent, commitContent), fsio.GitStatusModified, nil
}

func (g *localGit) refContents(ref string) (map[string]string, error) {
	hash, err := g.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	commit, err := g.repo.CommitObject(hash)
	if err != nil {
		return nil, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	err = tree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return err
		}
		out[f.Name] = content
		return nil
	})
	return out, err
}

func (g *localGit) refContent(ref, path string) (string, bool, error) {
	hash, err := g.resolveRef(ref)
	if err != nil {
		return "", false, err
	}
	commit, err := g.repo.CommitObject(hash)
	if err != nil {
		return "", false, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", false, err
	}
	file, err := tree.File(path)
	if err != nil {
		return "", false, nil
	}
	content, err := file.Contents()
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

func (g *localGit) currentContents(staged bool) (map[string]string, error) {
	if staged {
		return g.indexContents()
	}
	out, err := g.refContents("HEAD")
	if err != nil {
		out = map[string]string{}
	}
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, err
	}
	status, err := wt.Status()
	if err != nil {
		return nil, err
	}
	for path, s := range status {
		if s.Worktree == git.Deleted || s.Staging == git.Deleted {
			delete(out, path)
			continue
		}
		content, exists, err := g.currentContent(path, false)
		if err != nil {
			return nil, err
		}
		if !exists {
			delete(out, path)
			continue
		}
		out[path] = content
	}
	return out, nil
}

func (g *localGit) currentContent(path string, staged bool) (string, bool, error) {
	if staged {
		contents, err := g.indexContents()
		if err != nil {
			return "", false, err
		}
		content, ok := contents[path]
		return content, ok, nil
	}
	b, err := readFileBytes(filepath.Join(g.base, path))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(b), true, nil
}

func (g *localGit) indexContents() (map[string]string, error) {
	idx, err := g.repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range idx.Entries {
		blob, err := g.repo.BlobObject(e.Hash)
		if err != nil {
			return nil, err
		}
		r, err := blob.Reader()
		if err != nil {
			return nil, err
		}
		buf := new(strings.Builder)
		_, readErr := readAll(buf, r)
		closeErr := r.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out[e.Name] = buf.String()
	}
	return out, nil
}

func diffContentMaps(from, to map[string]string) []fsio.GitFile {
	paths := map[string]struct{}{}
	for p := range from {
		paths[p] = struct{}{}
	}
	for p := range to {
		paths[p] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	out := make([]fsio.GitFile, 0, len(ordered))
	for _, p := range ordered {
		fromContent, fromExists := from[p]
		toContent, toExists := to[p]
		if fromExists && toExists && fromContent == toContent {
			continue
		}
		out = append(out, fsio.GitFile{Path: p, Status: statusFromExistence(fromExists, toExists)})
	}
	return out
}

func statusFromExistence(fromExists, toExists bool) fsio.GitStatus {
	switch {
	case !fromExists && toExists:
		return fsio.GitStatusAdded
	case fromExists && !toExists:
		return fsio.GitStatusDeleted
	default:
		return fsio.GitStatusModified
	}
}

// resolveRef resolves a ref string (HEAD, branch, tag, or hash) to a hash.
func (g *localGit) resolveRef(ref string) (plumbing.Hash, error) {
	if ref == "HEAD" {
		head, err := g.repo.Head()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		return head.Hash(), nil
	}
	// Try as full hash.
	if h := plumbing.NewHash(ref); h != plumbing.ZeroHash {
		if _, err := g.repo.CommitObject(h); err == nil {
			return h, nil
		}
	}
	// Try as branch / tag reference (short name).
	if r, err := g.repo.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		return r.Hash(), nil
	}
	if r, err := g.repo.Reference(plumbing.NewTagReferenceName(ref), true); err == nil {
		return r.Hash(), nil
	}
	return plumbing.ZeroHash, fmt.Errorf("cannot resolve ref %q", ref)
}

// matchGlobs reports whether path matches any glob pattern via doublestar
// (issue 005: the ad-hoc matcher only handled exact paths, **, and /** suffix;
// patterns like src/*.go silently failed). Now uses the same glob engine as
// the filesystem Glob path.
func matchGlobs(patterns []string, path string) bool {
	for _, p := range patterns {
		if p == "**" {
			return true
		}
		if ok, _ := doublestar.Match(p, path); ok {
			return true
		}
	}
	return false
}

// unifiedDiff produces a minimal unified diff between two text versions.
// This is a simple line-based diff (not a full Myers algorithm) sufficient for
// showing before/after content; it marks removed/added lines.
func unifiedDiff(path, oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", path)
	fmt.Fprintf(&b, "+++ b/%s\n", path)
	// Simple approach: show common prefix, then removed, then added.
	i := 0
	for i < len(oldLines) && i < len(newLines) && oldLines[i] == newLines[i] {
		b.WriteString(" " + oldLines[i] + "\n")
		i++
	}
	// Removed lines (from old).
	for j := i; j < len(oldLines); j++ {
		if containsString(newLines[i:], oldLines[j]) {
			break
		}
		b.WriteString("-" + oldLines[j] + "\n")
	}
	// Added lines (from new).
	for j := i; j < len(newLines); j++ {
		if containsString(oldLines[i:], newLines[j]) {
			break
		}
		b.WriteString("+" + newLines[j] + "\n")
	}
	// Common suffix.
	oldIdx := len(oldLines) - 1
	newIdx := len(newLines) - 1
	suffix := []string{}
	for oldIdx > i && newIdx > i && oldLines[oldIdx] == newLines[newIdx] {
		suffix = append([]string{oldLines[oldIdx]}, suffix...)
		oldIdx--
		newIdx--
	}
	for _, l := range suffix {
		b.WriteString(" " + l + "\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func containsString(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}
