package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jixoai/jixomd/backend/local"
	"github.com/jixoai/jixomd/contract"
	"github.com/jixoai/jixomd/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// newMCPCmd creates the `mcp` subcommand — starts an MCP server exposing
// jixomd's capabilities (expand, resolve, skill_file_tree, skill_read_file).
func newMCPCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	var httpAddr string
	cmd := &cobra.Command{
		Use:   "mcp [--http ADDR]",
		Short: "Start an MCP server (stdio or HTTP)",
		Long: `Start a Model Context Protocol server that exposes jixomd's
capabilities as MCP tools:

  expand           Expand a Markdown document (doc mode)
  resolve          Resolve directives (batch mode)
  skill_file_tree  List embedded skill documentation files
  skill_read_file  Read an embedded skill documentation file

By default, runs over stdio (for use as an MCP stdio server).
Use --http to run a Streamable HTTP server instead.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCP(cmd.Context(), stdin, stdout, stderr, httpAddr)
		},
	}
	cmd.Flags().String("http", "", "start a Streamable HTTP server on this address (e.g. :8080)")
	return cmd
}

// runMCP starts the MCP server.
func runMCP(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, httpAddr string) error {
	server := newMCPServer()

	if httpAddr != "" {
		// Streamable HTTP mode.
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		mux := http.NewServeMux()
		mux.Handle("/mcp", handler)
		fmt.Fprintf(stderr, "jixomd mcp: listening on %s/mcp\n", httpAddr)
		httpServer := &http.Server{Addr: httpAddr, Handler: mux}
		go func() {
			<-ctx.Done()
			httpServer.Shutdown(context.Background())
		}()
		return httpServer.ListenAndServe()
	}

	// stdio mode (default).
	return server.Run(ctx, &mcp.StdioTransport{})
}

// newMCPServer builds the MCP server with all tools registered.
func newMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "jixomd",
		Title:   "jixomd",
		Version: "0.1.4",
	}, nil)

	// Tool: expand — expand a Markdown document.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "expand",
		Description: "Expand jixomd directives in a Markdown document. Directives like [src/**](@FILE) are resolved into real content.",
	}, expandToolHandler)

	// Tool: resolve — resolve a batch of directives.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "resolve",
		Description: "Resolve a batch of jixomd directives. Each directive has id, target, directive (mode), optional params and bang.",
	}, resolveToolHandler)

	// Tool: skill_file_tree — list embedded skill files.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_file_tree",
		Description: "List all embedded jixomd skill documentation files.",
	}, skillTreeToolHandler)

	// Tool: skill_read_file — read an embedded skill file.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "skill_read_file",
		Description: "Read an embedded jixomd skill documentation file by path.",
	}, skillReadToolHandler)

	return s
}

// ── Tool handlers ──

// expandArgs is the input schema for the expand tool.
type expandArgs struct {
	Doc      string `json:"doc" jsonschema_description:"The Markdown document containing jixomd directives to expand"`
	BaseDir  string `json:"baseDir,omitempty" jsonschema_description:"Base directory for path resolution"`
	MaxDepth int    `json:"maxDepth,omitempty" jsonschema_description:"Max recursive injection depth (0 = default 8)"`
}

type expandResult struct {
	Expanded string `json:"expanded"`
}

func expandToolHandler(ctx context.Context, req *mcp.CallToolRequest, in expandArgs) (*mcp.CallToolResult, expandResult, error) {
	io := local.New(in.BaseDir)
	out, err := core.Expand(ctx, io, in.Doc, core.Options{
		BaseDir:  in.BaseDir,
		MaxDepth: in.MaxDepth,
	})
	if err != nil {
		return nil, expandResult{}, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out}},
	}, expandResult{Expanded: out}, nil
}

// resolveArgs is the input schema for the resolve tool.
type resolveArgs struct {
	Directives []contract.Directive `json:"directives" jsonschema_description:"Array of directives to resolve"`
	BaseDir    string               `json:"baseDir,omitempty" jsonschema_description:"Base directory for path resolution"`
}

type resolveOutput struct {
	Blocks []contract.Block `json:"blocks"`
}

func resolveToolHandler(ctx context.Context, req *mcp.CallToolRequest, in resolveArgs) (*mcp.CallToolResult, resolveOutput, error) {
	io := local.New(in.BaseDir)
	dirs := make([]core.Directive, len(in.Directives))
	for i, r := range in.Directives {
		dirs[i] = core.Directive{
			ID:     r.ID,
			Target: r.Target,
			Mode:   r.Directive,
			Bang:   r.Bang,
			Params: r.Params,
		}
	}
	resolved, err := core.ResolveDirectives(ctx, io, dirs, core.Options{BaseDir: in.BaseDir})
	if err != nil {
		return nil, resolveOutput{}, err
	}
	blocks := make([]contract.Block, len(resolved))
	for i, rb := range resolved {
		blocks[i] = contract.Block{ID: rb.ID, Block: rb.Block}
	}
	out, _ := json.MarshalIndent(blocks, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(out)}},
	}, resolveOutput{Blocks: blocks}, nil
}

// skillTreeArgs — no input needed.
type skillTreeArgs struct{}

type skillTreeResult struct {
	Files []string `json:"files"`
}

func skillTreeToolHandler(ctx context.Context, req *mcp.CallToolRequest, in skillTreeArgs) (*mcp.CallToolResult, skillTreeResult, error) {
	var files []string
	if err := listSkillFilesToSlice(&files); err != nil {
		return nil, skillTreeResult{}, err
	}
	out := strings.Join(files, "\n")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out}},
	}, skillTreeResult{Files: files}, nil
}

// skillReadArgs is the input schema for skill_read_file.
type skillReadArgs struct {
	Path string `json:"path" jsonschema_description:"Path to the skill file (e.g. SKILL.md, references/cli.md)"`
}

type skillReadResult struct {
	Content string `json:"content"`
}

func skillReadToolHandler(ctx context.Context, req *mcp.CallToolRequest, in skillReadArgs) (*mcp.CallToolResult, skillReadResult, error) {
	full := "skillsdata/" + strings.TrimPrefix(in.Path, "skillsdata/")
	b, err := skillsFS.ReadFile(full)
	if err != nil {
		return nil, skillReadResult{}, fmt.Errorf("skill file not found: %s", in.Path)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, skillReadResult{Content: string(b)}, nil
}

// listSkillFilesToSlice walks the embedded FS and collects file paths.
func listSkillFilesToSlice(out *[]string) error {
	return walkSkillFS(func(rel string) {
		*out = append(*out, rel)
	})
}

// walkSkillFS walks the embedded skills FS and calls fn for each file.
func walkSkillFS(fn func(relPath string)) error {
	return fsWalk(skillsFS, "skillsdata", fn)
}

// fsWalk is separated for testability.
func fsWalk(fsys interface{ ReadDir(string) ([]os.DirEntry, error) }, root string, fn func(string)) error {
	entries, err := fsys.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := root + "/" + e.Name()
		if e.IsDir() {
			if err := fsWalk(fsys, full, fn); err != nil {
				return err
			}
		} else {
			rel := full[len("skillsdata/"):]
			fn(rel)
		}
	}
	return nil
}

// Suppress unused import warning (os is used by fsWalk's interface).
var _ = os.DirFS
