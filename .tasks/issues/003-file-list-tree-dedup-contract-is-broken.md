# `FILE_LIST` / `FILE_TREE` do not participate in dedup correctly

These modes emit a fixed id derived from `mode + target` and bypass the dedup
table entirely, so repeated identical blocks never collapse to `REF`. The id is
also too weak because it ignores the resolved file set and params.

Evidence:
- [`core/core.go:193`](../core/core.go:193) to [`core/core.go:215`](../core/core.go:215)
- [`core/core.go:398`](../core/core.go:398) to [`core/core.go:421`](../core/core.go:421)

Observed:
- Two `FILE_LIST` directives over the same pattern both render full blocks with
  the same id instead of the second becoming `REF`.
- Different params (`dot=true` vs default) still reuse the same id.

Impact:
- Token savings and content identity are both wrong for list/tree outputs.

Suggested fix:
- Compute identity from normalized params plus the resolved file set, and route
  these modes through the same dedup path as `FILE` / `INJECT`.

----
2026-06-16T17:02:39Z
Accepted and fixed. FILE_LIST / FILE_TREE now route through the dedup table (like FILE/INJECT) instead of always emitting a full block. The id is computed by `listTreeID(mode, target, params, paths)` which incorporates mode + target + normalized params + the sorted resolved file set, so:
- two identical list/tree directives → second emits REF
- differing params (dot, prefix, ignore) → distinct ids, no false dedup

Because this lives in the same dedup scope as FILE/INJECT, it also dedups correctly across a resolve batch.

Verified by unit `TestResolveDirectives_FileListDedupToRef`, `TestResolveDirectives_FileTreeDedupToRef`, `TestResolveDirectives_FileListDifferentParamsNoDedup`, and BDD `regressions.feature: resolve dedups identical FILE_LIST to REF`.
