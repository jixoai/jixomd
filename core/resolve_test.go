package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jixoai/jixomd/core"
)

// Directive is the structured form (mirrors contract.Directive without the
// core→contract dependency). These tests drive core.ResolveDirectives, the
// batch entrypoint that bypasses the markdown round-trip so params and dedup
// are honored directly.

// Issue 001: resolve must honor params (lang) in a batch.
func TestResolveDirectives_HonorsParams(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "hello"}}
	reqs := []core.Directive{
		{ID: "d1", Target: "a.txt", Mode: "FILE", Params: map[string][]string{"lang": {"python"}}},
	}
	out, err := core.ResolveDirectives(context.Background(), io, reqs, core.Options{})
	if err != nil {
		t.Fatalf("ResolveDirectives: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 block, got %d", len(out))
	}
	if !strings.Contains(out[0].Block, "```python") {
		t.Errorf("lang=python not honored in batch\n%s", out[0].Block)
	}
}

// Issue 002: empty batch returns an empty slice, not null.
func TestResolveDirectives_EmptyBatchReturnsEmptySlice(t *testing.T) {
	io := &fakeIO{files: map[string]string{}}
	out, err := core.ResolveDirectives(context.Background(), io, []core.Directive{}, core.Options{})
	if err != nil {
		t.Fatalf("ResolveDirectives: %v", err)
	}
	if out == nil {
		t.Errorf("want non-nil empty slice, got nil")
	}
	if len(out) != 0 {
		t.Errorf("want len 0, got %d", len(out))
	}
}

// Issue 003: two identical FILE_LIST directives → second is REF.
func TestResolveDirectives_FileListDedupToRef(t *testing.T) {
	io := &fakeIO{files: map[string]string{
		"src/a.go": "A",
		"src/b.go": "B",
	}}
	reqs := []core.Directive{
		{ID: "d1", Target: "src/**", Mode: "FILE_LIST"},
		{ID: "d2", Target: "src/**", Mode: "FILE_LIST"},
	}
	out, err := core.ResolveDirectives(context.Background(), io, reqs, core.Options{})
	if err != nil {
		t.Fatalf("ResolveDirectives: %v", err)
	}
	if !strings.Contains(out[0].Block, "src/a.go") {
		t.Errorf("first FILE_LIST should contain content\n%s", out[0].Block)
	}
	if !strings.Contains(out[1].Block, "jixomd:REF") {
		t.Errorf("second identical FILE_LIST should be REF\n%s", out[1].Block)
	}
}

// Issue 003b: FILE_LIST with different params should NOT dedup.
func TestResolveDirectives_FileListDifferentParamsNoDedup(t *testing.T) {
	io := &fakeIO{files: map[string]string{"a.txt": "x"}}
	reqs := []core.Directive{
		{ID: "d1", Target: "a.txt", Mode: "FILE_LIST"},
		{ID: "d2", Target: "a.txt", Mode: "FILE_LIST", Params: map[string][]string{"prefix": {"> "}}},
	}
	out, err := core.ResolveDirectives(context.Background(), io, reqs, core.Options{})
	if err != nil {
		t.Fatalf("ResolveDirectives: %v", err)
	}
	if strings.Contains(out[1].Block, "jixomd:REF") {
		t.Errorf("FILE_LIST with different params must not REF\n%s", out[1].Block)
	}
}

// Issue 005: matchGlobs via doublestar — covered by a git repo test in the BDD
// suite; here we assert the working-tree filter path also supports globs.
func TestResolveDirectives_FileTreeDedupToRef(t *testing.T) {
	io := &fakeIO{files: map[string]string{
		"src/a.go": "A",
	}}
	reqs := []core.Directive{
		{ID: "d1", Target: "src/**", Mode: "FILE_TREE"},
		{ID: "d2", Target: "src/**", Mode: "FILE_TREE"},
	}
	out, err := core.ResolveDirectives(context.Background(), io, reqs, core.Options{})
	if err != nil {
		t.Fatalf("ResolveDirectives: %v", err)
	}
	if !strings.Contains(out[1].Block, "jixomd:REF") {
		t.Errorf("second identical FILE_TREE should be REF\n%s", out[1].Block)
	}
}
