# npm `resolve()` ignores `packed` and `algo`

The JS wrapper advertises `{ baseDir, packed, algo }`, but `resolve()` only
passes `--base` to the native binary and always speaks raw JSON.

Evidence:
- [`npm/jixomd/index.js:77`](../npm/jixomd/index.js:77) to [`npm/jixomd/index.js:93`](../npm/jixomd/index.js:93)

Impact:
- Consumers calling the published JS API cannot use packed transport from the
wrapper, even though the signature/doc comment says they can.

Suggested fix:
- Thread `packed` and `algo` through the CLI invocation, or remove them from the
  API surface until the wrapper supports them.

----
2026-06-16T17:02:39Z
Accepted and fixed. `npm/jixomd/index.js: resolve()` now threads `opts.packed` and `opts.algo` to the CLI (`--packed --algo <gzip|zstd>`), and encodes/decodes the wire format via a new `npm/jixomd/packed.js` codec (base64(gzip|zstd(json)) using Node's built-in zlib; zstd used when Node 22+ exposes `zlib.zstdCompressSync`, else gzip fallback). `packed.js` is added to package.json `files`.

Verified by `npm/test/resolve-packed.test.js` (raw + packed gzip round-trip against a real binary) and the existing packed BDD scenarios on the Go side.
