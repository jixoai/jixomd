package cli

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed all:skillsdata
var skillsFS embed.FS

// newSkillCmd creates the `skill` subcommand (alias: `skills`).
//   jixomd skill                → list all embedded skill files (tree)
//   jixomd skill <path>         → print the content of an embedded file
func newSkillCmd(out, errw io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill [path]",
		Short: "Read embedded skill documentation",
		Long: `Read the jixomd skill documentation embedded in the binary.

Without arguments, lists all available skill files.
With a path argument, prints the file content to stdout.

Examples:
  jixomd skill                      # list all files
  jixomd skill SKILL.md             # print the main skill doc
  jixomd skill references/cli.md    # print the CLI reference`,
		Args: cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listSkillFiles(out)
			}
			return readSkillFile(out, errw, args[0])
		},
	}
	return cmd
}

// newSkillsCmd creates the `skills` alias.
func newSkillsCmd(out, errw io.Writer) *cobra.Command {
	cmd := newSkillCmd(out, errw)
	cmd.Use = "skills [path]"
	cmd.Aliases = []string{"skill"}
	cmd.Hidden = false
	cmd.Hidden = true // hidden alias of 'skill'
	return cmd
}

// listSkillFiles walks the embedded skillsdata/ FS and prints each file path.
func listSkillFiles(out io.Writer) error {
	var paths []string
	err := fs.WalkDir(skillsFS, "skillsdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Strip the "skillsdata/" prefix.
		rel := strings.TrimPrefix(p, "skillsdata/")
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintln(out, p)
	}
	return nil
}

// readSkillFile reads a single file from the embedded FS and prints it.
func readSkillFile(out, errw io.Writer, path string) error {
	// Try both "path" and "skillsdata/path" to be forgiving.
	full := "skillsdata/" + strings.TrimPrefix(path, "skillsdata/")
	b, err := skillsFS.ReadFile(full)
	if err != nil {
		fmt.Fprintf(errw, "jixomd: skill file not found: %s\n", path)
		return fmt.Errorf("not found: %s", path)
	}
	out.Write(b)
	return nil
}
