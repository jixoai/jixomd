# `resolve` drops directive params

`resolveBatch` rebuilds each request as `[target](@MODE)` and discards the
incoming `params` map before calling `core.Expand`.

Evidence:
- [`cli/cli.go:256`](../cli/cli.go:256) to [`cli/cli.go:295`](../cli/cli.go:295)

Observed:
- A request with `params.lang=python` still expands as the file extension
  language (`txt`), not `python`.

Impact:
- Batch tool-call requests cannot honor output-shaping or glob-control params
  from the wire contract.

Suggested fix:
- Preserve `Params` when synthesizing the temporary directive, or bypass the
  markdown round-trip and resolve the contract type directly.

----
2026-06-16T17:02:39Z
Accepted and fixed. Root cause was deeper than reported: `resolveBatch` synthesized each request as `[target](@MODE)` text and ran it through the markdown parser, which (a) dropped `Params` and (b) required a string round-trip that couldn't carry structured values. We added `core.ResolveDirectives(ctx, io, []core.Directive, opts)` — a structured batch entrypoint that passes `Directive.Params` directly to the resolver, bypassing markdown entirely. `cli.resolveBatch` now maps `contract.Directive` → `core.Directive` and calls it.

A second root cause surfaced during the fix: the wire `Params` type was `map[string][]string`, which rejects the natural JSON form `{"lang":"python"}`. We introduced a custom `contract.Params` type with `UnmarshalJSON` that accepts string / []string / number / bool and normalizes to []string, so JSON callers can use the ergonomic single-value form.

Verified by BDD `regressions.feature: resolve batch honors lang param` (block "d1" contains ```python) and unit `TestResolveDirectives_HonorsParams`.
