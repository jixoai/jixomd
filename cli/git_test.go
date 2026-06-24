package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gitEnv holds git config so commits work in CI without a global identity.
var gitEnv = []string{
	"GIT_AUTHOR_NAME=test",
	"GIT_AUTHOR_EMAIL=test@test",
	"GIT_COMMITTER_NAME=test",
	"GIT_COMMITTER_EMAIL=test@test",
}

// runGit runs a git command in the workspace with the test identity.
func (t *testCtx) runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = t.workspace
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %v\n%s", args, err, out)
	}
	return nil
}

// aGitRepositoryInitialized sets up a fresh git repo and an initial empty commit.
func (t *testCtx) aGitRepositoryInitialized() error {
	if err := t.runGit("init", "-q"); err != nil {
		return err
	}
	if err := t.runGit("add", "-A"); err != nil {
		return err
	}
	// An initial commit so HEAD exists. Use a placeholder file if none present.
	placeholder := filepath.Join(t.workspace, ".gitkeep")
	if err := os.WriteFile(placeholder, []byte(""), 0o644); err != nil {
		return err
	}
	if err := t.runGit("add", ".gitkeep"); err != nil {
		return err
	}
	if err := t.runGit("commit", "-q", "-m", "initial"); err != nil {
		return err
	}
	return t.runGit("branch", "-M", "main")
}

// aTrackedFileCommitted writes a file with the given content, stages and
// commits it.
func (t *testCtx) aTrackedFileCommitted(path, content string) error {
	full := filepath.Join(t.workspace, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	if err := t.runGit("add", path); err != nil {
		return err
	}
	return t.runGit("commit", "-q", "-m", "add "+path)
}

// theFileIsModifiedTo overwrites a file with new content (uncommitted).
func (t *testCtx) theFileIsModifiedTo(path, content string) error {
	full := filepath.Join(t.workspace, path)
	return os.WriteFile(full, []byte(content), 0o644)
}

func (t *testCtx) aGitBranchCheckedOutFromCurrentBranch(branch string) error {
	return t.runGit("checkout", "-q", "-b", branch)
}

func (t *testCtx) iCheckOutGitBranch(branch string) error {
	return t.runGit("checkout", "-q", branch)
}

func (t *testCtx) iStageFile(path string) error {
	return t.runGit("add", path)
}

func (t *testCtx) iDeleteFile(path string) error {
	return os.Remove(filepath.Join(t.workspace, path))
}
