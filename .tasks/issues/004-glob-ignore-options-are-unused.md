# `GlobOptions.Ignore` and `GlobOptions.IgnoreFiles` are ignored

`core.globPaths` forwards `ignore` and `ignoreFiles`, but the local backend
never consumes them. `backend/local.Glob` only loads a single `.gitignore` and
applies `Dot`.

Evidence:
- [`core/core.go:222`](../core/core.go:222) to [`core/core.go:231`](../core/core.go:231)
- [`backend/local/local.go:44`](../backend/local/local.go:44) to [`backend/local/local.go:88`](../backend/local/local.go:88)

Observed:
- `[**](@FILE?ignore=a.txt)` still includes `a.txt`.

Impact:
- The documented glob-control contract is incomplete, and hosts cannot exclude
  files through the advertised params.

Suggested fix:
- Apply `opts.Ignore` and `opts.IgnoreFiles` in the backend matcher, or remove
  the fields from the public contract until they are real.

----
2026-06-16T17:02:39Z
Accepted and fixed. `backend/local.Glob` now compiles and applies both `opts.Ignore` (inline patterns via `gitignore.CompileIgnoreLines`) and `opts.IgnoreFiles` (custom ignore-file paths via `CompileIgnoreFile`), in addition to the existing `.gitignore` load. All ignore sources are stacked and checked during the walk.

Verified by BDD `regressions.feature: FILE honors ignore param` ([*.txt](@FILE?ignore=b.txt) excludes b.txt).
