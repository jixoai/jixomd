# jixomd SDK Reference (TypeScript/JavaScript)

## Install

```bash
npm install jixomd
```

The matching platform binary (`@jixo/md-{os}-{arch}`) is installed automatically via `optionalDependencies`.

## API

### `expand(doc, opts?) → string`

Expand a Markdown string (doc mode).

```ts
import { expand } from 'jixomd';

const out = expand('# Doc\n[src/**](@FILE)', { baseDir: '.' });
```

**Options:**

| Option | Type | Description |
|---|---|---|
| `baseDir` | `string` | Base directory for path resolution |
| `maxDepth` | `number` | Max recursive injection depth (default 8) |

### `expandFile(filePath, opts?) → string`

Expand a Markdown file. Returns the expanded text. When `opts.output` is set, also writes to that path.

```ts
import { expandFile } from 'jixomd';

const out = expandFile('prompt.md', { baseDir: '.', output: 'prompt.gen.md' });
```

**Options:** same as `expand`, plus:

| Option | Type | Description |
|---|---|---|
| `output` | `string` | Write output to this path |

### `resolve(directives, opts?) → Block[]`

Resolve a batch of directives (tool-call mode). Returns an array of blocks.

```ts
import { resolve } from 'jixomd';

const blocks = resolve([
  { id: 'd1', target: 'src/**', directive: 'FILE', params: { lang: 'ts' } },
  { id: 'd2', target: 'app.ts', directive: 'GIT_DIFF' },
]);
// → [{ id: 'd1', block: '<!-- jixomd:START ... -->...' }, ...]
```

**Directive type:**

```ts
interface Directive {
  id: string;
  target: string;           // file path, glob, or "commit:path"
  directive: string;        // FILE, INJECT, FILE_LIST, FILE_TREE, GIT_FILE, GIT_DIFF
  bang?: boolean;           // force full (skip dedup)
  params?: Record<string, string | string[] | number | boolean>;
}
```

**Options:**

| Option | Type | Description |
|---|---|---|
| `baseDir` | `string` | Base directory for path resolution |
| `packed` | `boolean` | Use base64(gzip\|zstd(json)) transport |
| `algo` | `'gzip' \| 'zstd'` | Compression algo for packed mode (default gzip) |

### `binaryPath() → string`

Resolve the native binary path. Resolution order:
1. `JIXOMD_BINARY_PATH` env var
2. `require.resolve('@jixo/md-{slug}/jixomd')` — installed optional dependency
3. Local workspace fallback (`../jixomd-{slug}/jixomd`)

## Types

```ts
interface Block {
  id: string;
  block: string;
}

interface ExpandOptions {
  baseDir?: string;
  maxDepth?: number;
  output?: string;  // expandFile only
}

interface ResolveOptions {
  baseDir?: string;
  packed?: boolean;
  algo?: 'gzip' | 'zstd';
}
```

## Environment variables

| Variable | Purpose |
|---|---|
| `JIXOMD_BINARY_PATH` | Use a locally-built binary (dev override) |

## Platform packages

Binary distributed via per-platform npm packages (optionalDependencies):

| Package | Platform |
|---|---|
| `@jixo/md-darwin-arm64` | macOS Apple Silicon |
| `@jixo/md-darwin-x64` | macOS Intel |
| `@jixo/md-linux-arm64` | Linux ARM64 |
| `@jixo/md-linux-x64` | Linux x86-64 |
| `@jixo/md-win-arm64` | Windows ARM64 |
| `@jixo/md-win-x64` | Windows x86-64 |
