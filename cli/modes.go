package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ModeInfo describes a jixomd directive mode for help output.
type ModeInfo struct {
	Name        string
	Syntax      string
	Description string
	Example     string
}

// allModes returns the full list of directive modes for help/`jixomd help @FILE`.
func allModes() []ModeInfo {
	return []ModeInfo{
		{
			Name:        "FILE",
			Syntax:      `[path](@FILE)`,
			Description: "File content in a code fence, with auto-detected language.",
			Example:     `[src/main.go](@FILE)`,
		},
		{
			Name:        "INJECT",
			Syntax:      `[path](@INJECT)`,
			Description: "Inline content as-is. The content is recursively expanded (directives inside it are resolved).",
			Example:     `[notes.md](@INJECT)`,
		},
		{
			Name:        "FILE_LIST",
			Syntax:      `[glob](@FILE_LIST)`,
			Description: "Matched file paths, one per line. Useful for quick directory overviews.",
			Example:     `[src/**/*.ts](@FILE_LIST)`,
		},
		{
			Name:        "FILE_TREE",
			Syntax:      `[glob](@FILE_TREE)`,
			Description: "Tree view with ├── └── box-drawing connectors.",
			Example:     `[src/**](@FILE_TREE)`,
		},
		{
			Name:        "GIT_FILE",
			Syntax:      `[path](@GIT_FILE)`,
			Description: "Working-tree or commit content, annotated with git status (M/A/D/R). Use commit:path for commit-view.",
			Example:     `[app.ts](@GIT_FILE)  or  [HEAD:src/*.go](@GIT_FILE)`,
		},
		{
			Name:        "GIT_DIFF",
			Syntax:      `[path](@GIT_DIFF)`,
			Description: "Unified diff vs HEAD, a base ref, a left..right range, or parent commit. Shows what changed.",
			Example:     `[app.ts](@GIT_DIFF)  or  [src/**](@GIT_DIFF?compare=main..feature)`,
		},
	}
}

// modeMap is a quick lookup for `jixomd help @FILE`.
func modeMap() map[string]ModeInfo {
	modes := allModes()
	m := make(map[string]ModeInfo, len(modes))
	for _, mode := range modes {
		m[mode.Name] = mode
	}
	return m
}

// printModeHelp prints the help for a specific mode (e.g. `jixomd help @FILE`).
func printModeHelp(out io.Writer, modeName string) bool {
	modes := modeMap()
	mode, ok := modes[strings.ToUpper(modeName)]
	if !ok {
		return false
	}
	fmt.Fprintf(out, "@%s\n\n", mode.Name)
	fmt.Fprintf(out, "  Syntax:      %s\n", mode.Syntax)
	fmt.Fprintf(out, "  Description: %s\n", mode.Description)
	fmt.Fprintf(out, "  Example:     %s\n", mode.Example)
	fmt.Fprintln(out)
	return true
}

// printAllModes prints all modes (for `jixomd help @MODES` or the root help).
func printAllModes(out io.Writer) {
	modes := allModes()
	fmt.Fprintln(out, "Modes:")
	for _, m := range modes {
		fmt.Fprintf(out, "  %-12s %s\n", "@"+m.Name, m.Description)
	}
}

// modeNamesForHelp returns mode names sorted alphabetically.
func modeNamesForHelp() []string {
	modes := allModes()
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = m.Name
	}
	sort.Strings(names)
	return names
}
