---
name: jixomd
description: >-
  Markdown expansion engine for AI prompt engineering. Use when Codex needs to
  embed project files, file trees, git diffs, or directory listings into a
  Markdown document via directive syntax like [src/**](@FILE) or
  [HEAD:src/*.go](@GIT_DIFF). Covers: (1) authoring .meta.md prompt files with
  jixomd directives, (2) running `jixomd` CLI to expand them, (3) using the
  JavaScript/TypeScript SDK (expand, expandFile, resolve) programmatically,
  (4) understanding dedup/REF markers and output shaping params.
---

# jixomd

A pure Markdown expansion engine. Directives in `[target](@MODE)` link form are
expanded into real content (files, trees, diffs). The output keeps
`<!-- jixomd:START ... -->` / `<!-- jixomd:END ... -->` markers so the AI can
correlate blocks.

## Directive syntax

```markdown
[src/main.go](@FILE)           → file content in a code fence
[notes.md](@INJECT)            → inline content (recursively expanded)
[src/**/*.ts](@FILE_LIST)      → paths, one per line
[src/**](@FILE_TREE)           → tree view (├── └──)
[app.ts](@GIT_FILE)            → working-tree content with (M) status
[app.ts](@GIT_DIFF)            → unified diff vs HEAD
[src/**](@GIT_DIFF?base=main)  → working-tree diff vs main
[src/**](@GIT_DIFF?compare=main..feature) → ref range diff
[HEAD:src/*.go](@GIT_FILE)     → commit-view content at HEAD
[a.txt](@FILE!lang=python)     → ! = force full (skip dedup); ?lang= shapes output
```

- Targets are globs. Backtick-wrap names with special chars: `` [`weird name`](@FILE) ``.
- `commit:path` syntax (e.g. `HEAD:src/*.go`) selects a git commit view.
- `@GIT_DIFF` supports `base=<ref>` and `compare=left..right`; `compare` is not a `@GIT_FILE` law.
- Directives inside code blocks / inline code / HTML comments are **never** expanded (AST-based, not regex).

## Quick start (CLI)

```bash
# Expand → stdout
jixomd file.md

# Expand → file.gen.md, then watch for changes (multi-file + glob supported)
jixomd '*.meta.md' --watch

# Expand from stdin
cat file.md | jixomd -

# Tool-call mode: JSON directive array → JSON block array
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve

# Packed transport (gzip/zstd)
echo '<packed>' | jixomd resolve --packed --algo zstd
```

For the full CLI reference, see [references/cli.md](references/cli.md).

## Quick start (SDK)

```ts
import { expand, expandFile, resolve } from 'jixomd';

const out = expand('# Doc\n[src/**](@FILE)', { baseDir: '.' });
const out2 = expandFile('prompt.md', { baseDir: '.' });
const blocks = resolve([{ id: 'd1', target: 'src/**', directive: 'FILE' }]);
```

For the full SDK reference (types, options, packed mode), see [references/sdk.md](references/sdk.md).

## Dedup & REF

Within one expand, identical content emits a full block first, then `REF`:

```html
<!-- jixomd:START id=af3c mode=FILE path="src/foo.ts" -->
…full content…
<!-- jixomd:END id=af3c -->

<!-- jixomd:START id=af3c mode=FILE path="src/foo.ts" /-->
<!-- jixomd:REF -->
<!-- jixomd:END id=af3c -->
```

`!` forces a full copy in an isolated scope.

## Output shaping params (query string on the directive)

| Param | Effect |
|---|---|
| `lang=<lang>` | Override fence language |
| `map_ext_<ext>_lang=<lang>` | Map file extension to language |
| `prefix=<str>` | Prefix each line (or N spaces) |
| `filepath=<name>` | Override the title path |
| `ignore=<pattern>` | Exclude files matching pattern |
| `ignoreFiles=<file>` | Additional ignore file |
| `gitignore=false` | Disable .gitignore |
| `noFound.msg=<text>` | Custom no-match message |
| `staged=true` | Git: use staged (index) version |
| `base=<ref>` | Git diff: compare working tree/index against ref |
| `compare=left..right` | Git diff: compare two refs (two-dot only) |
