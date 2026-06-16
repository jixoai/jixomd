# Commit-target git matching only supports a tiny subset of globs

`matchGlobs` only handles exact paths, `**`, and `/.../**` prefixes. Patterns
like `src/*.go` or `src/**/foo.go` do not match, even though the SPEC describes
commit targets as file patterns.

Evidence:
- [`backend/local/git.go:169`](../backend/local/git.go:169) to [`backend/local/git.go:190`](../backend/local/git.go:190)
- [`backend/local/git.go:277`](../backend/local/git.go:277) to [`backend/local/git.go:295`](../backend/local/git.go:295)

Observed:
- `[HEAD:src/*.go](@GIT_FILE)` resolves to `<!-- jixomd: no files for ... -->`
  even when the commit contains `src/a.go`.

Impact:
- Git modes silently miss valid files and the advertised pattern syntax is not
  actually supported.

Suggested fix:
- Reuse the backend glob engine for commit-file selection instead of the ad hoc
  `matchGlobs` helper.

----
2026-06-16T17:02:39Z
Accepted and fixed in both locations:
- `backend/local/git.go: matchGlobs` now uses `doublestar.Match` (the same engine as the filesystem Glob), so `src/*.go`, `src/**/foo.go`, etc. match correctly in commit-view file selection.
- `core.matchAny` (working-tree filter) was also ad-hoc. Since core must stay pure (SPEC §6: no doublestar import), we wrote a self-contained pure glob matcher (`globMatch` / `globSegs` / `globSegment`) supporting **, *, ?, and literal segments.

Verified by BDD `regressions.feature: @GIT_FILE with a commit glob matches via doublestar` ([HEAD:src/*.go] matches src/a.go, excludes docs/b.md).
