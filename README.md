<div align="center">

# jixomd

A pure Markdown expansion engine for AI prompt engineering.

[![CI](https://github.com/jixoai/jixomd/actions/workflows/ci.yml/badge.svg)](https://github.com/jixoai/jixomd/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/jixomd)](https://www.npmjs.com/package/jixomd)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

## What is this?

jixomd resolves **directives** embedded in Markdown documents into real
project content — source files, file trees, git diffs — producing a complete,
self-contained document ready to feed an LLM.

```markdown
Review this codebase:

[src/**](@FILE_TREE)

[src/main.go](@FILE)

Here's what changed: [main.go](@GIT_DIFF)
```

→ expands to a Markdown file with the tree, file content, and diff inlined.

This is the declarative alternative to having an AI call tools at runtime to
fetch context: you declare what it needs upfront, the engine resolves it
before the prompt is processed.

## Install

```bash
npm install jixomd
npx jixomd file.md
```

Or build from source (requires Go 1.24+):

```bash
npm run build    # → bin/jixomd
```

## Usage

### CLI

```bash
jixomd file.md                        # expand → stdout
jixomd '*.meta.md' --watch            # multi-file watch → *.gen.md
echo '<json>' | jixomd resolve        # tool-call batch mode (JSON in/out)
```

### SDK (TypeScript)

```ts
import { expand, resolve } from 'jixomd';

const doc = expand('[src/**](@FILE)', { baseDir: '.' });
const blocks = resolve([{ id: 'd1', target: 'src/**', directive: 'FILE' }]);
```

## Directives

| Syntax | Effect |
|---|---|
| `[path](@FILE)` | File content in a code fence |
| `[path](@INJECT)` | Inline content (recursively expanded) |
| `[glob](@FILE_LIST)` | Matched paths, one per line |
| `[glob](@FILE_TREE)` | Tree view with `├──` `└──` connectors |
| `[path](@GIT_FILE)` | Working-tree or commit content + status |
| `[path](@GIT_DIFF)` | Unified diff vs HEAD, `base`, or `compare` range |
| `[path](@FILE!)` | `!` forces full copy (skips dedup) |
| `[HEAD:src/*.go](@GIT_FILE)` | Commit-view via `commit:path` syntax |
| `[src/**](@GIT_DIFF?base=main)` | Working tree diff vs `main` |
| `[src/**](@GIT_DIFF?compare=main..feature)` | Ref range diff |

Directives inside code blocks, inline code, or HTML comments are never
expanded (parsed via a real Markdown AST, not a regex).

**Shaping params** (query string): `?lang=python`, `?prefix=> `,
`?ignore=node_modules/`, `?noFound.msg=empty`, `?map_ext_ts_lang=typescript`.
Git diff params include `?base=main`, `?base=main&staged=true`, and
`?compare=main..feature` (two-dot ranges only).

## Documentation

- **[skills/jixomd/](skills/jixomd/)** — full CLI + SDK reference (directive
  syntax, all flags, TypeScript types, markers, dedup behavior)
- **[SPEC.md](SPEC.md)** — architecture, design rationale, specification

## Architecture

```
cli ──▶ backend (local: os + doublestar + gitignore + go-git) ──▶ core (pure)
                                                                       ▲
                                                     depends only on injected IO
```

**core** is pure Go — zero filesystem, network, git, or time dependencies.
All side effects flow through an injected `IO` interface, making it testable
in isolation and retargetable to other backends (e.g. a virtual filesystem).

## Development

```bash
npm run jixomd -- file.md    # debug build + run
npm run test                 # go vet + go test + npm e2e
npm run build                # release-grade binary
npm run publish              # build all platforms + publish npm workspace
```

Tests are BDD-driven ([godog](https://github.com/cucumber/godog) Gherkin
features under `features/`).

## License

MIT
