# jixomd (npm)

A **pure Markdown expansion engine**. Resolve `[target](@FILE)` directives into real content — for prompt engineering, codegen, and AI pipelines.

This npm package wraps the native [jixomd](https://github.com/jixoai/jixomd) binary (Go). It downloads the correct platform binary at install time.

## Install

```bash
npm install jixomd
# or
npx jixomd file.md
```

## CLI

```bash
# Expand a document (doc mode)
jixomd file.md > file.gen.md
jixomd file.md --watch                  # re-expand on change

# Resolve directives as JSON (tool_call mode)
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve
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

The postinstall downloader respects:

| Variable | Purpose |
|---|---|
| `JIXOMD_SKIP_DOWNLOAD` | Skip binary download (offline / dev) |
| `JIXOMD_BINARY_PATH` | Use a locally-built binary at this path |
| `JIXOMD_VERSION` | Pin a release version |
| `JIXOMD_REPO` | Override `owner/repo` (e.g. a fork) |
| `JIXOMD_MIRROR` | Base URL replacing `https://github.com` (for mirrors / proxies) |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` | Standard proxy env (download uses curl when set) |

### Behind a proxy / mirror

```bash
# Corporate proxy
HTTPS_PROXY=http://corp-proxy:8080 npm install jixomd

# GitHub mirror (e.g. ghproxy)
JIXOMD_MIRROR=https://ghproxy.com/https://github.com npm install jixomd
```

## License

MIT
