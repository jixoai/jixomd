# `resolve` returns `null` for an empty batch

`resolveBatch` returns a `nil` slice for `len(reqs) == 0`, and `json.Marshal`
turns that into `null`.

Evidence:
- [`cli/cli.go:256`](../cli/cli.go:256) to [`cli/cli.go:259`](../cli/cli.go:259)
- [`cli/cli.go:227`](../cli/cli.go:227) to [`cli/cli.go:243`](../cli/cli.go:243)

Observed:
- `jixomd resolve` with `[]` on stdin prints `null`.

Impact:
- The CLI breaks its own "JSON array in, JSON array out" contract on the empty
  case.

Suggested fix:
- Return an empty slice (`[]contract.Block{}`) instead of `nil`.

----
2026-06-16T17:02:39Z
Accepted and fixed. `resolveBatch` now returns `make([]contract.Block, 0, len(reqs))` (non-nil) for empty input, and `core.ResolveDirectives` likewise returns a non-nil empty slice. `json.Marshal` of an empty non-nil slice produces `[]`, not `null`.

Verified by BDD `regressions.feature: resolve with empty array returns []` (stdout does not contain "null") and unit `TestResolveDirectives_EmptyBatchReturnsEmptySlice`.
