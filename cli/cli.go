// Package cli is the testable CLI layer. main (cmd/jixomd) is a thin wrapper
// that calls cli.Run(args, stdin, stdout, stderr) and maps its exit code.
//
// Built on Cobra for structured, colored help generation.
package cli

import (
	"context"
	"io"
)

// ExitCode values per SPEC §4.3.
const (
	ExitOK       = 0
	ExitUsage    = 2 // parse / contract error
	ExitIO       = 3 // resolve IO error
	ExitInternal = 4
)

// Run executes one CLI invocation. args excludes the program name.
// Returns an exit code; writes human-facing output to out, diagnostics to errw.
func Run(args []string, stdin io.Reader, out, errw io.Writer) int {
	return RunContext(context.Background(), args, stdin, out, errw)
}

// RunContext is like Run but with a caller-supplied context (used by tests to
// cancel --watch).
func RunContext(ctx context.Context, args []string, stdin io.Reader, out, errw io.Writer) int {
	root := newRootCmd(stdin, out, errw)
	root.cmd.SetArgs(args)
	if err := root.cmd.ExecuteContext(ctx); err != nil {
		// Cobra returns errors for usage issues; map to exit codes.
		if root.exitCode != 0 {
			return root.exitCode
		}
		return ExitUsage
	}
	return root.exitCode
}
