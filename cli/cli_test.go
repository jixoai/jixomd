package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/jixoai/jixomd/cli"
	"github.com/jixoai/jixomd/contract"
)

// testCtx holds per-scenario state. Each scenario gets a fresh temp workspace.
type testCtx struct {
	workspace string
	stdin     string
	exitCode  int
	stdout    string
	stderr    string
	watch     *watchHandle
}

func (t *testCtx) reset() {
	if t.workspace != "" {
		_ = os.RemoveAll(t.workspace)
	}
	dir, err := os.MkdirTemp("", "jixomd-bdd-*")
	if err != nil {
		panic(err)
	}
	t.workspace = dir
	t.stdin = ""
	t.exitCode = -1
	t.stdout = ""
	t.stderr = ""
}

// --- workspace fixtures ---

func tableData(table *godog.Table) []*messages.PickleTableRow {
	if len(table.Rows) == 0 {
		return nil
	}
	return table.Rows[1:]
}

func (t *testCtx) aWorkspaceWithFiles(table *godog.Table) error {
	for _, row := range tableData(table) {
		full := filepath.Join(t.workspace, row.Cells[0].Value)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(row.Cells[1].Value), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (t *testCtx) aDocumentContaining(name, content string) error {
	full := filepath.Join(t.workspace, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func (t *testCtx) iProvideOnStdin(content string) error {
	t.stdin = content
	return nil
}

// --- run (synchronous) ---

func (t *testCtx) run(cmd string) error {
	args := shellSplit(cmd)
	if len(args) > 0 && args[0] == "jixomd" {
		args = args[1:]
	}
	args = t.injectBase(args)
	var out, errw bytes.Buffer
	t.exitCode = cli.Run(args, strings.NewReader(t.stdin), &out, &errw)
	t.stdout = out.String()
	t.stderr = errw.String()
	return nil
}

func (t *testCtx) runWithStdin(_ string, stdin string) error {
	t.stdin = stdin
	return t.run("jixomd resolve")
}

func (t *testCtx) runWithMaxDepth(cmd string, depth int) error {
	return t.run(cmd + " --max-depth " + strconv.Itoa(depth))
}

// injectBase inserts --base <workspace> after the (optional) subcommand, before
// any positional arg.
func (t *testCtx) injectBase(args []string) []string {
	out := append([]string{}, args...)
	insertAt := 0
	if len(out) > 0 && (out[0] == "resolve" || out[0] == "expand") {
		insertAt = 1
	}
	res := append([]string{}, out[:insertAt]...)
	res = append(res, "--base", t.workspace)
	res = append(res, out[insertAt:]...)
	return res
}

// --- assertions ---

func (t *testCtx) exitCodeShouldBe(want int) error {
	if t.exitCode != want {
		return fmt.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", t.exitCode, want, t.stdout, t.stderr)
	}
	return nil
}

func (t *testCtx) stdoutShouldContain(s string) error {
	if !strings.Contains(t.stdout, s) {
		return fmt.Errorf("stdout missing %q\nstdout:\n%s", s, t.stdout)
	}
	return nil
}

func (t *testCtx) stdoutShouldNotContain(s string) error {
	if strings.Contains(t.stdout, s) {
		return fmt.Errorf("stdout unexpectedly contains %q\nstdout:\n%s", s, t.stdout)
	}
	return nil
}

func (t *testCtx) stdoutShouldContainExactlyTimes(s string, n int) error {
	got := strings.Count(t.stdout, s)
	if got != n {
		return fmt.Errorf("stdout contains %q %d times, want %d\nstdout:\n%s", s, got, n, t.stdout)
	}
	return nil
}

func (t *testCtx) stdoutValidJSONWithBlocks(table *godog.Table) error {
	var blocks []contract.Block
	if err := json.Unmarshal([]byte(strings.TrimSpace(t.stdout)), &blocks); err != nil {
		return fmt.Errorf("stdout not valid JSON: %v\n%s", err, t.stdout)
	}
	byID := map[string]contract.Block{}
	for _, b := range blocks {
		byID[b.ID] = b
	}
	for _, row := range tableData(table) {
		b, ok := byID[row.Cells[0].Value]
		if !ok {
			return fmt.Errorf("no block with id %q", row.Cells[0].Value)
		}
		if !strings.Contains(b.Block, row.Cells[1].Value) {
			return fmt.Errorf("block %q missing %q\n%s", row.Cells[0].Value, row.Cells[1].Value, b.Block)
		}
	}
	return nil
}

func (t *testCtx) blockShouldContain(id, s string) error {
	var blocks []contract.Block
	if err := json.Unmarshal([]byte(strings.TrimSpace(t.stdout)), &blocks); err != nil {
		return fmt.Errorf("stdout not valid JSON: %v\n%s", err, t.stdout)
	}
	for _, b := range blocks {
		if b.ID == id {
			if !strings.Contains(b.Block, s) {
				return fmt.Errorf("block %q missing %q\n%s", id, s, b.Block)
			}
			return nil
		}
	}
	return fmt.Errorf("no block with id %q", id)
}

func (t *testCtx) stdoutPackedWithBlocks(algo string, table *godog.Table) error {
	var blocks []contract.Block
	if err := contract.DecodePacked(strings.TrimSpace(t.stdout), contract.Algo(algo), &blocks); err != nil {
		return fmt.Errorf("stdout not valid packed(%s): %v\n%s", algo, err, t.stdout)
	}
	byID := map[string]contract.Block{}
	for _, b := range blocks {
		byID[b.ID] = b
	}
	for _, row := range tableData(table) {
		b, ok := byID[row.Cells[0].Value]
		if !ok {
			return fmt.Errorf("no block with id %q", row.Cells[0].Value)
		}
		if !strings.Contains(b.Block, row.Cells[1].Value) {
			return fmt.Errorf("block %q missing %q\n%s", row.Cells[0].Value, row.Cells[1].Value, b.Block)
		}
	}
	return nil
}

func (t *testCtx) iProvidePackedInputWith(algo string, table *godog.Table) error {
	var dirs []contract.Directive
	for _, row := range tableData(table) {
		dirs = append(dirs, contract.Directive{
			ID:        row.Cells[0].Value,
			Target:    row.Cells[1].Value,
			Directive: row.Cells[2].Value,
		})
	}
	packed, err := contract.EncodePacked(dirs, contract.Algo(algo))
	if err != nil {
		return err
	}
	t.stdin = packed
	return nil
}

// --- output-file polling (watch + general) ---

func (t *testCtx) outputContainsWithinStep(dur, filename, want string) error {
	d := 2 * time.Second
	if dur != "" {
		var err error
		d, err = time.ParseDuration(dur)
		if err != nil {
			return err
		}
	}
	return t.outputContainsWithin(d, filename, want)
}

func (t *testCtx) outputNotContainsWithinStep(dur, filename, want string) error {
	d := 2 * time.Second
	if dur != "" {
		var err error
		d, err = time.ParseDuration(dur)
		if err != nil {
			return err
		}
	}
	return t.outputNotContainsWithin(d, filename, want)
}

// --- godog wiring ---

// Step patterns as double-quoted strings (backticks are literal in them).
var (
	stepIRun             = "^I run `([^`]+)`$"
	stepIRunWithStdin    = "^I run `([^`]+)` with stdin \"([^\"]*)\"$"
	stepIRunWithMaxDepth = "^I run `([^`]+)` with max depth (\\d+)$"
	stepStartWatch       = "^I (?:start|run) `([^`]+)` in the background$"
	stepOverwrite        = "^I overwrite \"([^\"]+)\" with \"([^\"]+)\"$"
	stepOverwriteTwice   = "^I overwrite \"([^\"]+)\" with \"([^\"]+)\" and then immediately \"([^\"]+)\"$"
	stepOutContains      = "^(?:within (\\d+[a-z]*) )?the output file \"([^\"]+)\" should contain \"([^\"]+)\"$"
	stepOutNotContains   = "^(?:within (\\d+[a-z]*) )?the output file \"([^\"]+)\" should not contain \"([^\"]+)\"$"
)

func InitializeScenario(ctx *godog.ScenarioContext) {
	t := &testCtx{}

	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		t.reset()
		return ctx, nil
	})
	ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		t.stopWatch()
		return ctx, nil
	})

	ctx.Step(`^a workspace with files$`, t.aWorkspaceWithFiles)
	ctx.Step(`^a document "([^"]+)" containing$`, t.aDocumentContaining)
	ctx.Step(`^I provide on stdin$`, t.iProvideOnStdin)
	ctx.Step(`^I provide on stdin "([^"]*)"$`, t.iProvideOnStdin)
	ctx.Step(stepIRun, t.run)
	ctx.Step(stepIRunWithStdin, t.runWithStdin)
	ctx.Step(stepIRunWithMaxDepth, t.runWithMaxDepth)
	ctx.Step(`^the exit code should be (\d+)$`, t.exitCodeShouldBe)
	ctx.Step(`^stdout should contain "([^"]*)"$`, t.stdoutShouldContain)
	ctx.Step(`^stdout should not contain "([^"]*)"$`, t.stdoutShouldNotContain)
	ctx.Step(`^stdout should contain "([^"]*)" exactly (\d+) times?$`, t.stdoutShouldContainExactlyTimes)
	ctx.Step(`^stdout should be valid JSON with blocks$`, t.stdoutValidJSONWithBlocks)
	ctx.Step(`^block "([^"]*)" should contain "([^"]*)"$`, t.blockShouldContain)
	ctx.Step(`^stdout should be packed \((gzip|zstd)\) with blocks$`, t.stdoutPackedWithBlocks)
	ctx.Step(`^I provide packed input \((gzip|zstd)\) with directives$`, t.iProvidePackedInputWith)

	// Watch-mode steps (SPEC §5).
	ctx.Step(stepStartWatch, t.startWatchBackground)
	ctx.Step(stepOverwrite, t.overwriteFile)
	ctx.Step(stepOverwriteTwice, t.overwriteTwice)
	ctx.Step(stepOutContains, t.outputContainsWithinStep)
	ctx.Step(stepOutNotContains, t.outputNotContainsWithinStep)
	ctx.Step(`^the watch log should contain "([^"]*)"$`, t.watchStderrShouldContain)
	ctx.Step(`^the watch log should not contain "([^"]*)"$`, t.watchStderrShouldNotContain)

	// Git-mode steps (SPEC §1.2 @GIT_FILE / @GIT_DIFF).
	ctx.Step(`^a git repository initialized in the workspace with an initial commit$`, t.aGitRepositoryInitialized)
	ctx.Step(`^a tracked file "([^"]+)" with content "([^"]+)" committed$`, t.aTrackedFileCommitted)
	ctx.Step(`^the file "([^"]+)" is modified to "([^"]+)"$`, t.theFileIsModifiedTo)
}

// shellSplit is a minimal splitter for the command strings used in features.
func shellSplit(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case (r == ' ' || r == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
