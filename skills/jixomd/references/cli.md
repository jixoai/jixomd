# jixomd CLI Reference

## Commands

### `jixomd <paths...>` (doc mode, default)

Expand directives in Markdown file(s) → stdout or `--output`.

```bash
jixomd file.md                      # → stdout
jixomd file.md --output out.md      # → file
jixomd -                            # stdin → stdout
jixomd '*.meta.md' --watch          # multi-file watch, each → .gen.md
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--output <path>` / `--out` | stdout | Output file (single-input only) |
| `--watch` / `-W` | off | Watch for changes, re-expand automatically |
| `--watch-debounce <dur>` | 200ms | Debounce window for --watch (e.g. 50ms, 1s) |
| `--base <dir>` | cwd / file dir | Base directory for path resolution + watch root |
| `--max-depth <n>` | 8 | Max recursive injection depth |

**Watch mode:** accepts multiple paths and globs. Each input generates its own
`<input>.gen.md`. Output files are automatically excluded from the input set to
prevent generation loops (a `.gen.md` matched by `*.md` won't be re-processed).

### `jixomd resolve` (tool-call mode)

JSON directive array in → JSON block array out. Always batch (single = array of one).

```bash
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve

# Packed transport
echo '<packed>' | jixomd resolve --packed --algo zstd
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--packed` | off | Wrap I/O as base64(gzip\|zstd(json)) |
| `--algo <gzip\|zstd>` | gzip | Compression algo for --packed |
| `--base <dir>` | cwd | Base directory |

**Request format:**

```jsonc
[
  {"id":"d1","target":"src/**","directive":"FILE","params":{"lang":"ts"}},
  {"id":"d2","target":"src/**","directive":"GIT_DIFF","params":{"compare":"main..feature"},"bang":true}
]
```

- `directive`: the MODE (FILE, INJECT, FILE_LIST, FILE_TREE, GIT_FILE, GIT_DIFF)
- `params`: shaping/glob params (lang, prefix, ignore, etc.)
- `bang`: `true` = force full (skip dedup)

**Response format:**

```jsonc
[
  {"id":"d1","block":"<!-- jixomd:START id=... mode=FILE ... -->...<!-- jixomd:END ... -->"},
  {"id":"d2","block":"..."}
]
```

Dedup crosses items within one resolve call (same Expand scope).

**Exit codes:** `0` ok · `2` parse/contract error · `3` IO error (degradation comments don't count) · `≥4` internal.

## Directive modes

| Mode | Input | Output |
|---|---|---|
| `@FILE` | file path/glob | content in code fence |
| `@INJECT` | file path/glob | content as-is (recursively expanded) |
| `@FILE_LIST` | glob | matched paths, one per line |
| `@FILE_TREE` | glob | tree view with ├── └── connectors |
| `@GIT_FILE` | file or `commit:path` | working-tree or commit content + status |
| `@GIT_DIFF` | file, `commit:path`, `base`, or `compare` | unified diff vs HEAD/base/range or parent commit |

Git diff params:

| Param | Effect |
|---|---|
| `staged=true` | Diff staged/index version instead of working tree |
| `base=<ref>` | Diff working tree/index against ref |
| `compare=left..right` | Diff two refs; two-dot range only |

`compare` is only a `@GIT_DIFF` law. `@GIT_FILE` remains working-tree content
or `commit:path` content.

## Markers

Every expanded block is wrapped:

```html
<!-- jixomd:START id=<hash> mode=<MODE> path="<path>" [force=1] -->
<content>
<!-- jixomd:END id=<hash> -->
```

Repeat (dedup): self-closing START + REF:

```html
<!-- jixomd:START id=<hash> mode=<MODE> path="<path>" /-->
<!-- jixomd:REF -->
<!-- jixomd:END id=<hash> -->
```

## Installation

```bash
npm install jixomd        # installs matching platform binary via optionalDependencies
npx jixomd file.md
```

Or build from source:

```bash
npm run build             # → bin/jixomd (release-grade)
npm run jixomd -- file.md # debug build + run
```

Set `JIXOMD_BINARY_PATH` to use a locally-built binary.
