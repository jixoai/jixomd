# jixomd

**[English](#english) · [中文](#中文)**

---

<a id="english"></a>

# jixomd (English)

A **pure** Markdown expansion engine. Read a Markdown document containing directives like `[src/**](@FILE)`, resolve them to real content (files, diffs, …), and emit the completed document.

Migrated from the `gen-prompt` engine in [`jixo2`](https://github.com/jixoai/jixo2), rewritten in Go with an explicit IO boundary so it can run standalone **or** be driven by an agent pipeline via a tool-call contract.

See **[SPEC.md](SPEC.md)** for the full specification.

## Quick start

```bash
# Build
go build -o jixomd ./cmd/jixomd

# Expand a document (default: doc mode, raw output to stdout)
jixomd file.md

# Write to a file instead
jixomd file.md --output file.gen.md

# Read from stdin
cat file.md | jixomd -

# Watch mode: re-expand on any input change (≈ jixo G --watch)
jixomd file.md --watch                  # writes file.gen.md
jixomd file.md --watch --watch-debounce 100ms
```

### Directives

A directive is a standard Markdown link whose URL starts with `@`:

```markdown
Here is a file: [src/main.go](@FILE)

Inject raw: [notes.md](@INJECT)

Force a fresh copy even if already shown: [shared.h](@FILE!)
```

- `[target](@FILE)` — file content in a code fence.
- `[target](@INJECT)` — inline content as-is (recursively expanded).
- `[target](@FILE!)` — the `!` forces a full copy (skips dedup; isolated scope).
- Targets are globs: `[src/**/*.ts](@FILE)`, backtick-wrapped: `` [`weird name`](@FILE) ``.

**Literal contexts are exempt:** directives inside code blocks, inline code, or HTML comments are never expanded (parsed via a real Markdown AST, not a regex — this is the correctness fix over the original).

### Deduplication (token savings)

Within one `Expand`, the first occurrence of a content id emits the full block; repeats emit a `REF`:

```
<!-- jixomd:START id=af3c path="src/foo.ts" -->
…full content…
<!-- jixomd:END id=af3c -->

<!-- later, same content: -->
<!-- jixomd:START id=af3c /-->
<!-- jixomd:REF -->
<!-- jixomd:END id=af3c -->
```

Add `!` to always emit full content.

## Two commands

| Command | In → Out | Use |
|---|---|---|
| `jixomd <file>` | **Markdown → Markdown** (raw) | Standalone; replaces `jixo G`. Hosts do whole-document replace. |
| `jixomd resolve` | **JSON array → JSON array** | tool-call granularity. Hosts splice each block back, preserving the original prompt byte-for-byte. |

### resolve (batch contract)

Always JSON array in, JSON array out (a single directive = array of one):

```bash
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve
# [{"id":"d1","block":"<!-- jixomd:START ... -->..."}]

# Packed transport (gzip or zstd) for remote tool calls:
echo '<packed>' | jixomd resolve --packed --algo zstd
```

Dedup crosses items within one resolve call (same `Expand` scope).

Exit codes: `0` ok · `2` parse/contract error · `3` IO error (degradation comments don't count) · `≥4` internal.

## Architecture

```
cli ──▶ backend (local: os + doublestar + gitignore) ──▶ core (pure)
                                                              ▲
                                              depends only on injected IO + goldmark
```

- **core** (`core/`): pure. Zero `os`/`net`/`git`/`time.Now`. Depends only on `goldmark` + the `IO` interface. This is the "wasm host-import" seam.
- **io** (`io/`): the `IO` / `Git` / `HTTP` capability interfaces. `ErrUnsupported` declines optional capabilities (degrades to a comment).
- **contract** (`contract/`): the wire types + packed codec for `resolve`.
- **backend/local** (`backend/local/`): the only place that touches the real filesystem. `Git()`/`HTTP()` return `ErrUnsupported` (TODO: go-git).
- **cli** (`cli/`): `Run`/`RunContext` + doc/resolve/watch.

Swap the backend to retarget (e.g. a future mindos VFS backend implements `IO` and declines `Git`).

## Testing (BDD)

Driven by [godog](https://github.com/cucumber/godog) Gherkin features under `features/`:

```bash
go test ./...                     # all tests, including BDD
JIXOMD_BDD_OFF=1 go test ./...    # skip BDD
```

15 scenarios across `cli_doc`, `resolve`, `dedup`, `watch`, plus core unit tests. See [SPEC §10](SPEC.md) for coverage.

## Status

Implemented: `@FILE`, `@INJECT`, dedup/REF/`!`, doc + resolve + watch, packed transport (gzip/zstd), AST-based literal exemption.

TODO (see SPEC §8): `@FILE_TREE`/`@FILE_LIST`/`@GIT_FILE`/`@GIT_DIFF`, `@PLUGIN`, fine-grained incremental watch, mindos VFS backend, wasm build.

## GOROOT note

On this machine the `GOROOT` env var points at a stale Homebrew Cellar. Prefix Go commands with `env -u GOROOT`, or `unset GOROOT` in your shell profile.

---
---

<a id="中文"></a>

# jixomd(中文)

一个**纯粹**的 Markdown 扩展引擎。读取含指令(如 `[src/**](@FILE)`)的 Markdown 文档,把它们就地展开为真实内容(文件、diff 等),输出完整的文档。

从 [`jixo2`](https://github.com/jixoai/jixo2) 的 `gen-prompt` 引擎迁移而来,用 Go 重写,并定义了显式的 IO 边界——既能独立运行,也能经 tool_call 契约被 agent pipeline 驱动。

完整规范见 **[SPEC.md](SPEC.md)**。

## 快速上手

```bash
# 编译
go build -o jixomd ./cmd/jixomd

# 展开文档(默认:doc 模式,raw 输出到 stdout)
jixomd file.md

# 改为写到文件
jixomd file.md --output file.gen.md

# 从 stdin 读取
cat file.md | jixomd -

# watch 模式:任意输入变更即重展开(≈ jixo G --watch)
jixomd file.md --watch                  # 写到 file.gen.md
jixomd file.md --watch --watch-debounce 100ms
```

### 指令

指令在语法上就是一个 URL 以 `@` 开头的标准 Markdown 链接:

```markdown
这是一个文件: [src/main.go](@FILE)

原样注入: [notes.md](@INJECT)

强制发完整副本(即便已出现过): [shared.h](@FILE!)
```

- `[target](@FILE)` —— 文件内容包进代码栅栏。
- `[target](@INJECT)` —— 内容原样内联(会递归展开)。
- `[target](@FILE!)` —— 尾巴的 `!` 强制发完整内容(跳过去重;隔离作用域)。
- target 支持 glob:`[src/**/*.ts](@FILE)`,反引号包裹:`` [`奇怪的名字`](@FILE) ``。

**字面上下文豁免:**代码块、行内代码、HTML 注释里的指令**永不**展开(用真正的 Markdown AST 解析,而非正则——这是相对原版 jixo2 的正确性修复)。

### 去重(省 token)

一次 `Expand` 内,某 content id 首次出现发完整块;重复出现发 `REF`:

```
<!-- jixomd:START id=af3c path="src/foo.ts" -->
…完整内容…
<!-- jixomd:END id=af3c -->

<!-- 之后,同一份内容: -->
<!-- jixomd:START id=af3c /-->
<!-- jixomd:REF -->
<!-- jixomd:END id=af3c -->
```

加 `!` 则永远发完整内容。

## 两个命令

| 命令 | 输入 → 输出 | 用途 |
|---|---|---|
| `jixomd <file>` | **Markdown → Markdown**(raw) | 独立运行;替代 `jixo G`。宿主整段替换。 |
| `jixomd resolve` | **JSON 数组 → JSON 数组** | tool_call 粒度。宿主逐块 splice 回原文,逐字节保真。 |

### resolve(batch 契约)

始终 JSON 数组进、JSON 数组出(单条 = 长度 1 的数组):

```bash
echo '[{"id":"d1","target":"src/**","directive":"FILE"}]' | jixomd resolve
# [{"id":"d1","block":"<!-- jixomd:START ... -->..."}]

# 打包传输(gzip 或 zstd),适合远程 tool_call:
echo '<packed>' | jixomd resolve --packed --algo zstd
```

一次 resolve 调用内,dedup 跨条目生效(同一个 `Expand` 作用域)。

退出码:`0` 成功 · `2` 解析/契约错误 · `3` IO 错误(降级注释不算)· `≥4` 内部错误。

## 架构

```
cli ──▶ backend (local: os + doublestar + gitignore) ──▶ core (纯)
                                                              ▲
                                              只依赖注入的 IO + goldmark
```

- **core**(`core/`):纯。零 `os`/`net`/`git`/`time.Now`。只依赖 `goldmark` + `IO` 接口。这就是「wasm host-import」的缝。
- **io**(`io/`):`IO` / `Git` / `HTTP` 能力接口。`ErrUnsupported` 表示某可选能力不可用(降级为注释)。
- **contract**(`contract/`):wire 类型 + resolve 的打包编解码。
- **backend/local**(`backend/local/`):唯一接触真实文件系统的地方。`Git()`/`HTTP()` 返回 `ErrUnsupported`(TODO:go-git)。
- **cli**(`cli/`):`Run`/`RunContext` + doc/resolve/watch。

换 backend 即可换宿主(例如未来的 mindos VFS backend 实现 `IO` 并对 `Git` 返回 unsupported)。

## 测试(BDD)

由 [godog](https://github.com/cucumber/godog) Gherkin 场景驱动,见 `features/`:

```bash
go test ./...                     # 全部测试(含 BDD)
JIXOMD_BDD_OFF=1 go test ./...    # 跳过 BDD
```

15 个场景,覆盖 `cli_doc`、`resolve`、`dedup`、`watch`,外加 core 单测。详见 [SPEC §10](SPEC.md)。

## 状态

已实现:`@FILE`、`@INJECT`、dedup/REF/`!`、doc + resolve + watch、打包传输(gzip/zstd)、基于 AST 的字面豁免。

TODO(见 SPEC §8):`@FILE_TREE`/`@FILE_LIST`/`@GIT_FILE`/`@GIT_DIFF`、`@PLUGIN`、细粒度增量 watch、mindos VFS backend、wasm 构建。

## GOROOT 注意

本机 `GOROOT` 环境变量指向旧的 Homebrew Cellar。Go 命令需加 `env -u GOROOT` 前缀,或在 shell profile 里 `unset GOROOT`。
