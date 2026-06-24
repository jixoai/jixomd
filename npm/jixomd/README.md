# jixomd (npm)

A **pure Markdown expansion engine**. Resolve `[target](@FILE)` directives into real content — for prompt engineering, codegen, and AI pipelines.

This npm package wraps the native [jixomd](https://github.com/jixoai/jixomd) binary (Go). The correct platform binary is installed automatically via optional dependencies (`@jixo/md-{os}-{arch}`).

## Install

```bash
npm install jixomd
# or
npx jixomd file.md
```

npm automatically installs the matching `@jixo/md-{os}-{arch}` platform package based on your OS and CPU — no download step, no postinstall script.

## CLI

```bash
# Expand a document (doc mode)
jixomd file.md > file.gen.md
jixomd file.md --watch                  # re-expand on change

# Resolve directives as JSON (tool_call mode)
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve
```

Git diff directives support branch/ref comparisons:

```markdown
[src/**](@GIT_DIFF?base=main)
[src/**](@GIT_DIFF?compare=main..feature)
```

See the [main README](https://github.com/jixoai/jixomd#readme) for the full directive syntax.

## JavaScript API

```js
const { expand, expandFile, resolve } = require('jixomd');

// Expand a Markdown string
const out = expand('# Doc\n[src/**](@FILE)', { baseDir: '.' });

// Expand a file
const out2 = expandFile('prompt.md', { baseDir: '.' });

// Resolve a directive batch (returns blocks)
const blocks = resolve([
  { id: 'd1', target: 'src/**', directive: 'FILE' },
]);
```

## Environment variables

| Variable | Purpose |
|---|---|
| `JIXOMD_BINARY_PATH` | Use a locally-built binary at this path (dev override) |

## Platform packages

The native binaries are distributed as separate npm packages, pulled in as optional dependencies:

| Package | Platform |
|---|---|
| `@jixo/md-darwin-arm64` | macOS Apple Silicon |
| `@jixo/md-darwin-x64` | macOS Intel |
| `@jixo/md-linux-arm64` | Linux ARM64 |
| `@jixo/md-linux-x64` | Linux x86-64 |
| `@jixo/md-win-arm64` | Windows ARM64 |
| `@jixo/md-win-x64` | Windows x86-64 |

## License

MIT
