# jixomd SPEC

> 状态:Draft v1 · 实现语言:Go · 定位:从 jixo2 `gen-prompt` 迁移而来的**独立** Markdown 扩展引擎

---

## 0. 目标与定位

jixomd 是一个**纯 Markdown 扩展引擎**:读入含指令的 Markdown,把指令(`[src/**](@FILE)`)就地展开为真实内容(文件、git diff 等),输出完整的 Markdown。

- **独立**:可单独打包成一个二进制;也可只复用其中某个部件(parser、merger、local backend)。
- **纯粹**:核心层零 IO,所有副作用经一个可注入的 `IO` 能力接口。等于「在写一个 wasm 包」——IO 边界显式定义。
- **多种用法**:① 独立 CLI(`jixomd expand file.md`,基本替代 jixo2 的 `jixo G`,含 `--watch`);② 经 tool_call 被宿主(如 agent prompt 预处理)调用,把客户端内容拼接进服务端 prompt;③ 作为 Go 库被嵌入。
- **不绑定任何具体宿主**。本文档不含 memoryai 等下游系统的内容。

### 设计原则(贯穿全文)

- **SRP**:core(纯变换) / resolve backend(IO) / cli(装配与 watch)三层职责单一。
- **DIP**:core 依赖 `IO` 抽象,不依赖 `os`/`git`/`net` 具体实现。
- **KISS / YAGNI**:对齐 jixo2 的**粗粒度**响应式(任意输入变更 → 全量重跑),不引入细粒度增量框架;`jixo:` 内置符号移除,改为 `@PLUGIN` 协议并推迟到 TODO。

---

## 1. 语法规范(Grammar)

> 本节是**稳定契约**。任何宿主(含服务端 detector)都可据此独立识别指令,无需 import jixomd 包。

### 1.1 指令形式与识别(基于 Markdown AST)

一条指令在语法上就是一个**标准 Markdown 链接**,其 URL 以 `@` 开头:

```
[target](@MODE!?params)
```

**识别规则(权威,由 jixomd 的 parser 实现)**:把文档解析为标准 Markdown AST(Go 用 [`goldmark`](https://github.com/yuin/goldmark),CommonMark 合规、纯解析无 IO),遍历 AST,凡满足下列条件的**链接节点**即为指令:

- 节点类型 = `link`;
- 其 destination(URL)匹配 `^@([A-Za-z][A-Za-z0-9_-]*)(!)?(\?.*)?$`。

各部分:

- `target` = 链接节点的**文本内容**(拼接其全部文本子节点;故 `` [`a/b/c`](@FILE) `` 的反引号包裹被自然还原为 `a/b/c`)。三种形态见 §1.3。
- `MODE` = URL 中 `@` 后的标识符。解析时**归一化**:`toUpperCase()` 且 `-` → `_`,故 `@GIT-DIFF` ≡ `@GIT_DIFF`。
- `!` = URL 标识符末尾的 `!`,**强制跳过 cache**(见 §3)。
- `params` = URL 中 `?` 之后的 query(URL-search 语法,见 §1.4)。

**字面上下文豁免(关键)**:指令仅在被解析为**链接节点**时生效。位于下列节点内部的文本是字面量,**永不**当作指令:

- 代码块 `code`(fenced / indented)
- 行内代码 `inlineCode`
- 原始 HTML `html`(含 HTML 注释 `<!-- ... -->`)

> 这正是正则方案的根本缺陷:正则会误匹配注释 / 代码块里的 `[..](@..)`。AST 方案天然正确——`<!-- [src/**](@FILE) -->` 在 AST 里是单个 `html` 节点,其内部的「链接」根本不被解析。

**解析与回填模型**:AST 仅用于**定位指令**(取其源码字节区间 span)。输出采用**字符串切片回填**:按 span 从后往前把指令替换为展开内容;非指令部分**逐字节保留**,绝不重新渲染 Markdown(避免破坏用户原文排版与空白)。

### 1.2 MODE 清单(对齐 jixo2)

| MODE | 语义 | resolve 产出 | 输出形态 |
|---|---|---|---|
| `@INJECT` | 原样内联内容 | 文本 | 纯文本(不包栅栏) |
| `@FILE` | 文件内容包进代码栅栏 | `{path, content}` 列表 | `\`path\`` + 围栏代码块 |
| `@FILE_TREE` | 匹配文件的树视图 | 文件路径列表 | `├──/└──` 树形文本 |
| `@FILE_LIST` | 仅文件路径清单 | 文件路径列表 | 路径逐行 |
| `@GIT_FILE` | git 视角的文件**内容**(工作区改动 或 指定 commit) | `{path, content, status}` | 同 `@FILE`,标题带 `(status)` |
| `@GIT_DIFF` | git 视角的 **diff** | `{path, diff, status}` | diff 围栏块,标题带 `(status)` |

未知 MODE → 输出 `<!-- jixomd: unknown mode <MODE> -->`(不报错,容错降级)。

### 1.3 target 三态

1. **glob / 路径**:`src/**/*.ts`、`a/b/c`、`` `a/b/c` ``(反引号包裹则去边界,用于含特殊字符)。
2. **URL**:`https://...` / `http://...` → 经 `IO.HTTP()` 拉取文本。
3. **`commit:path`(仅 git 类 MODE)**:`HEAD:**`、`abc123:src/*.js`。首个 `:` 之前为 git ref,之后为文件 pattern,多个 pattern 以逗号分隔。
   - **`plugin:val`(`@PLUGIN` 协议)**:**TODO,本期不实现**。见 §8。

> 注:原 jixo2 的内置符号(`jixo:coder`/`jixo:pwd`/`jixo:datetime`/`jixo:memory`)**全部移除**,统一由 `@PLUGIN` 协议(可插拔)替代。

### 1.4 params(URL-search 语法 `?k=v&k=v`)

两类参数:

**(A) glob / resolve 控制**(透传给 `IO.Glob` 与 git 解析):

| key | 类型 | 说明 |
|---|---|---|
| `gitignore` | bool | 是否遵守 `.gitignore`(`@FILE`/`@FILE_TREE`/`@INJECT` 默认 true,单文件路径默认 false) |
| `ignore` | string\|string[] | 额外忽略 pattern |
| `ignoreFiles` | string\|string[] | 自定义 ignore 文件 |
| `expandDirectories` | bool | `@FILE_TREE` 是否递归展开目录(默认 true) |
| `dot` / `deep` / `onlyFiles` / `onlyDirectories` / `absolute` / `caseSensitiveMatch` / `globstar` / `braceExpansion` / `extglob` / `baseNameMatch` / `followSymbolicLinks` | bool/int | 透传 glob 选项 |
| `staged` | bool | git 类:仅 staged(`@GIT_*` 工作区视角) |
| `ignore`(git) | string\|string[] | git 文件列表的二次过滤 pattern |

**(B) 输出塑形**(纯,在 MERGE 层生效):

| key | 说明 |
|---|---|
| `lang` | 强制代码栅栏语言 |
| `ext` | 同 `lang` 的别名(优先级低于 `lang`) |
| `map_ext_<ext>_lang` | 按文件扩展名映射语言,如 `map_ext_ts_lang=typescript` |
| `mime_<type>_lang` | URL 注入时按 MIME 映射语言 |
| `prefix` | 每行前缀(string 原样 / number 表示 N 个空格) |
| `filepath` | 覆盖输出标题里的路径名 |
| `spaces` | (仅 JSON 资产)序列化缩进 |
| `noFound` / `noFound.msg` / `noFound.prefix` / `noFound.suffix` | 无匹配时的占位输出;默认 `<!-- No files found for pattern: <p> -->` |

### 1.5 文档级纯变换

- **frontmatter 剥离**:文档顶部 `---\n...\n---`(YAML)在 AST 解析前作为前缀剥离(字符串级);`cwd`/`output` 等字段作为调用方参数(非内置符号)。
- **HTML 注释剥离**:在 AST 上,移除所有 `html` 注释节点(`<!-- ... -->`)的 span(与指令回填同一遍字符串处理)。因 §1.1 的指令识别本就跳过 `html` 子树,故注释内的「指令」既不生效、整段注释也被移除。
  - jixomd 自身产出的 `<!-- jixomd:... -->` 标记(见 §3)在 **MERGE 阶段**生成,晚于此剥离,故不会被移除。

---

## 2. 架构

三层 + 一个 `IO` 抽象。依赖方向单向:`cli → backend → core`。

```
┌─────────────────────────────────────────────────────────┐
│ cli           expand / resolve / --watch                │
│               (装配 + watch 循环 + fsnotify)            │
└───────────────────────┬─────────────────────────────────┘
                        ▼ 依赖
┌─────────────────────────────────────────────────────────┐
│ resolve backend  local(os+go-git+doublestar)           │
│                  (实现 IO 接口;watch 模式下兼做访问记录) │
│                  [未来: wasm host-import / mindos VFS]   │
└───────────────────────┬─────────────────────────────────┘
                        ▼ 依赖
┌─────────────────────────────────────────────────────────┐
│ core (纯)  Parse / Resolve-orchestration / Merge / Format│
│            零 os·net·git·time.Now;依赖 IO 抽象           │
└─────────────────────────────────────────────────────────┘
```

### 2.1 核心入口(纯函数)

```go
package jixomd

func Expand(ctx context.Context, io IO, doc string, opts Options) (string, error)

type Options struct {
    BaseDir  string      // 相对路径解析根;也作为 watch 的默认监听根
    MaxDepth int         // 递归注入深度上限,默认 8
    // dedup / 标记行为开关见 §3
}
```

`Expand` 是**唯一的纯入口**:输入文档字符串 + `IO` 能力 + 选项,输出展开后的文档。无任何隐式环境依赖。

### 2.2 IO 抽象(DIP 的 seam)

```go
type IO interface {
    // —— 文件系统(必需)——
    Stat(path string) (Entry, error)                  // 区分 file/dir
    ReadFile(path string) ([]byte, error)             // utf-8 文本
    Glob(pattern string, opts GlobOptions) ([]string, error) // 含 gitignore 全语义

    // —— 可选能力(缺失时对应 MODE 降级为注释,见 §2.4)——
    Git() (Git, error)   // 不可用返回 ErrUnsupported
    HTTP() (HTTP, error) // 不可用返回 ErrUnsupported
}

type Git interface {
    ChangedFiles() ([]GitFile, error)                                  // 工作区(含 index)改动 + status
    WorkingContent(path string, staged bool) (string, GitStatus, error)
    WorkingDiff(path string, staged bool) (string, GitStatus, error)   // vs HEAD
    FilesAtCommit(ref string, globs []string) ([]string, error)
    CommitContent(ref, path string) (string, GitStatus, error)         // git show ref:path
    CommitDiff(ref, path string) (string, GitStatus, error)            // 该 commit 引入的 diff(vs parent)
}

type HTTP interface {
    Get(url string) (body []byte, contentType string, err error)
}
```

- `IO.Glob` 的**全语义**(pattern 匹配 + 目录遍历 + gitignore)由 backend 实现;core 不自行遍历文件系统(保持纯)。
- **watch 模式**下,backend 在实现 `IO` 的同时**记录每次访问**(Stat/ReadFile/Glob 的入参与扫描根),供 cli 的 watch 层注册监听(见 §5)。即 backend 一物两用:真 IO + 依赖采集。这与 jixo2 `reactive-fs` 记录读取的思路一致,但**采集逻辑在 backend 而非 core**——core 不感知 watch。

### 2.3 递归与收敛

- `@INJECT` 一个 `.md` 文件、或 URL/资产返回含指令的内容时,**该内容被重新 PARSE** 并继续展开(服务端/客户端同此规则)。
- core 驱动到**不动点**,受 `MaxDepth`(默认 8)保护;超限 → 该处输出 `<!-- jixomd: max depth exceeded -->`,不 panic。

### 2.4 能力缺失的降级

- `IO.Git()` 返回 `ErrUnsupported` → 所有 `@GIT_*` 输出 `<!-- jixomd: git unsupported -->`。
- `IO.HTTP()` 返回 `ErrUnsupported` → URL target 输出 `<!-- jixomd: http unsupported -->`。
- 文件类 MODE 始终可用(文件系统是必需能力)。

---

## 3. 去重与标记契约(dedup + START/END + `!`)

为节省 token 并让下游(AI)能跨处关联「同一份内容」,每次注入都包裹**带 id 的标记**;同内容重复注入默认只发**引用**。

### 3.1 标记格式

```
<!-- 首次注入(完整内容) -->
<!-- jixomd:START id=<id> path="<path>" -->
<内容>
<!-- jixomd:END id=<id> -->

<!-- 同内容再次被请求(默认去重,只发引用) -->
<!-- jixomd:START id=<id> /-->
<!-- jixomd:REF -->
<!-- jixomd:END id=<id> -->
```

- `id` = 内容稳定哈希(对 `@FILE`/`@GIT_FILE` 取 `mode+path(+ref)`;对 `@INJECT` 取内容 hash)。同一 `id` 即同一份内容。
- `@FILE_TREE`/`@FILE_LIST` 的 `id` 基于 `mode+pattern+排序后的文件集`。
- 标记是 **jixomd 产出物**,晚于 §1.5 的注释剥离生成,故不会被剥掉。

### 3.2 去重作用域与默认行为

- 作用域 = **一次 `Expand` 调用**(一个文档一次;跨 build / 跨请求不复用)。
- 默认:**首处全量,余处 REF**。
- 去重键 = `id`(即内容等价性)。

### 3.3 `!` 强制跳过 cache

`[path](@FILE!)`:

1. **不查去重表**——永远发完整内容(即便同 `id` 已出现过)。
2. **独立 dedup 作用域**——该注入内容若含嵌套指令,在**隔离的 dedup 表**里递归解析,结果再注入主文档;避免与主文档标记串味。
3. 该次产出**仍带 START/END 标记**(带自己的 `id`),可被后续普通注入 REF 引用。

> `!` 仅对「会产出内容」的 MODE 有意义(`@FILE`/`@INJECT`/`@GIT_FILE`/`@GIT_DIFF`);对 `@FILE_TREE`/`@FILE_LIST` 等价于普通注入。

---

## 4. 契约与 CLI

### 4.1 两个命令,各司其职

| 命令 | 输入 | 输出 | 用途 |
|---|---|---|---|
| `jixomd <path>`(默认裸命令) | **Markdown 文档** | **Markdown 文档**(raw) | 独立本地用,等价 `jixo G`;服务端「整份 md 委托、整段替换」 |
| `jixomd resolve` | **JSON 数组**(directive 列表) | **JSON 数组**(block 列表) | tool_call 粒度;服务端「拆成列表、自行 splice、保护原文」 |

两者共享同一个 `core.Expand` 引擎与同一套 START/END 标记(§3);区别只在「文档进文档出」还是「指令数组进、块数组出」。

### 4.2 默认裸命令(doc 模式)= 独立 CLI

```
jixomd file.md                 # 读文件 → 展开全部指令 → stdout
jixomd file.md --output out.md # 同上,但写文件(--output / --out)
jixomd -                       # 从 stdin 读文档 → stdout
jixomd file.md --watch         # 监听变更,增量重跑(见 §5);默认输出 file.gen.md(或 --out)
```

- **始终 raw**:UTF-8 文本,直接打 stdout 或写文件。这是本地首要场景(替代 `jixo G`),**没有打包编码**。
- 默认输出文件名规则:无 `--output` → stdout;`--watch` 无 `--output` → `<input>.gen.md`。
- 二进制内容(如 `@FILE` 命中图片)→ 输出 `<!-- jixomd: binary skipped <path> -->`(跳过,见 §4.4)。

### 4.3 resolve 命令 = tool_call 粒度

**始终 JSON 数组进、JSON 数组出**。单条指令 = 长度为 1 的数组;**不区分 single/batch,统一 batch**。

```
# stdin 读 JSON 数组,stdout 吐 JSON 数组(raw)
echo '[{...}]' | jixomd resolve

# 同上,但 input/output 走 base64(gzip|zstd(json)) 外壳,适合 tool_call 远程传输
echo '<packed>' | jixomd resolve --packed
jixomd resolve --packed --algo zstd   # 默认 gzip,可选 zstd
```

**request(JSON 数组,每个元素一个 directive):**

```jsonc
[
  {"id":"d1","target":"src/**","directive":"FILE","params":{"lang":"ts"}},
  {"id":"d2","target":"HEAD:**","directive":"GIT_DIFF"}
]
// 单条就是长度 1 的数组
[{"id":"d0","target":"README.md","directive":"INJECT"}]
```

**response(JSON 数组,顺序与 request 对齐):**

```jsonc
[
  {"id":"d1","block":"<!-- jixomd:START id=af3c path=\"...\" -->\n```ts\n...\n```\n<!-- jixomd:END id=af3c -->"},
  {"id":"d2","block":"<!-- jixomd:START id=... -->\n```diff\n...\n```\n<!-- jixomd:END id=... -->"}
]
```

- `block` = 该 directive 经 MERGE + FORMAT 后的**完整带标记块**(含 START/END,见 §3)。宿主按 `id` 自行 splice 回原文对应位置(保护用户原文排版)。
- **dedup 作用域 = 整个数组**(一次 `resolve` 调用):数组内若多个 directive 解析到同一内容,第 2 个起发 REF(§3.2)。这要求 resolve 实现用**同一个 `Expand` 作用域**跑完整个数组,而非逐条独立。
- `--packed`:对整个 JSON(请求/响应)做 `base64(<algo>(json))` 外壳。`--algo gzip`(默认)|`zstd`。
- 退出码:`0` 成功;`2` JSON 解析/契约错误;`3` resolve IO 错误(纯降级注释如 git-unsupported 不算错误,仍在 `block` 里);`>=4` 内部错误。

### 4.4 二进制内容

- **doc 模式 / resolve raw**:`@FILE` 命中二进制(图片等)→ 跳过,输出 `<!-- jixomd: binary skipped <path> -->`。
- **resolve `--packed`**:二进制内容以**字段内 base64** 携带(block 内仍以占位注释呈现,但附 `data_b64` 字段供宿主按需取用)。

### 4.5 宿主侧 detector(分流)

宿主在把 prompt 交给下游处理前,需检测是否含指令。两种可选:

- **粗预筛(推荐)**:用廉价正则 `]\(@[A-Za-z]` 检测「是否可能含指令」。会误报(命中注释 / 代码块里的),但代价只是「多 delegate 一次」——jixomd 按权威 AST 规则判定后若无真实指令,原样返回。
- **精检测**:用任意 MD 解析器做与 §1.1 相同的链接节点判定(零误报)。

分流(宿主自选粒度):

- 0 处 → 原样放行。
- 1 处 或 N 处 → **两种皆可**:① 提取指令列表 → `resolve`(数组,宿主自行 splice,保护原文);② 整份 markdown → 默认裸命令(doc 模式,宿主整段替换)。
  - 选 ① 当宿主想精确保护 prompt 原文(只替换指令 span、其余逐字节保留);
  - 选 ② 当宿主不在意原文保真、只要最终展开结果。

宿主 detector 依据 §1.1 的 AST 识别规则(稳定契约)自行实现,**不 import jixomd 包**——彻底解耦。

---

## 5. Watch 模式

对齐 jixo2 的**粗粒度**响应式:任意被监听输入变更 → 防抖 → **全量重跑** + 单次 build 内 memoization。**不做**细粒度增量(只重建受影响输出)——YAGNI,留 TODO。

### 5.1 选型

- 监听:**`fsnotify`**(事实标准,跨平台 inotify/kqueue/ReadDirectoryChangesW)。**公开 API 非递归**,采用标准模式:`filepath.Walk` 遍历 → 每个 dir `Add` → 监听 `Create`/`Rename` 动态增删子目录 watch。
- 备选:`radovskyb/watcher`(轮询、开箱递归,网络盘更稳,代价 CPU/IO 略高)。默认走 fsnotify。

### 5.2 重跑循环(仅在 cli 层;core 不感知)

```
build():
    backend.accessesReset()
    out = core.Expand(ctx, backend, doc, opts)     # backend 边做 IO 边记录访问
    write(out)
    watchRoots = backend.accessedRoots()            # 本次 build 实际触碰的目录根
    ensureWatches(watchRoots)                       # fsnotify Add(动态递归)

watchLoop:
    on fsnotify event → debounce(100~300ms, 合并突发) → build()
```

- **memoization**:一次 build 内 `map[path]content` 去重重复读(作用域 = 单次 build)。
- **watch 根**:默认 = `Options.BaseDir` 递归;`backend.accessedRoots()` 提供更精确的作用域(可选优化,大仓时缩小监听面)。
- core 仍 100% 纯:不 import fsnotify;watch 是 cli 对 core 的反复调用 + 一个包了访问记录的 backend。

### 5.3 与去重(§3)的关系

dedup 作用域是**一次 `Expand`**;watch 每次重跑都是新的 `Expand`,故 dedup 表每次重建(不会跨 build 串)。两者正交。

---

## 6. 纯粹性约束(给实现者)

**core 包禁止:**

- `os` / `os/*`、`io/ioutil`、`path/filepath`(遍历)、`embed`
- `net` / `net/http`
- 任何 git 库(`go-git` 等)
- `time.Now()`(时间相关需求由调用方注入;当前 grammar 已无 `datetime` 类内置符号,core 无需时间)
- 全局可变状态(缓存必须显式传入或作用域内局部)

**core 仅可依赖:**

- 标准库 `strings` / `regexp` / `sort` / `strconv` / `unicode` / `path`(纯路径字符串处理)
- [`goldmark`](https://github.com/yuin/goldmark)(纯 Markdown AST 解析,无 IO)
- `context`(取消/超时,但不得用于 IO)
- 一个注入的 `IO` 实现 + `Options`

**包结构建议:**

```
jixomd/
  grammar/    指令正则、target/params 解析(纯)
  core/       Parse / Merge / Format + Expand 入口 + 契约类型(纯)
  io/         IO / Git / HTTP 接口定义 + ErrUnsupported(纯,仅接口)
  backend/
    local/    os + go-git + doublestar 实现 IO(含访问记录)
  cli/        expand / resolve / watch(装配层)
```

---

## 7. 从 jixo2 的迁移映射

| jixo2 概念 | jixomd 对应 | 说明 |
|---|---|---|
| `gen-prompt.ts:_gen_content` | `core.Expand` | 纯函数化;正则与回填逻辑保留 |
| `file-replacer` / `git-replacer` | core 内 mode 分派 + `IO` 调用 | 塑形逻辑(FILE 栅栏/FILE_TREE 树)迁入 core |
| `generateFileTree` | core(纯) | 不变 |
| `params-to-globby-options` | `GlobOptions` 映射(纯) | params → 选项 |
| `jixo-provider`(`jixo:*`) | **移除** → `@PLUGIN`(TODO §8) | 不再内置;可插拔协议 |
| `reactive-fs`(watch + memo) | cli watch 层(§5)+ backend 访问记录 | 粗粒度对齐;core 不含 |
| `simple-git`(shell out) | `backend/local` 用 `go-git`(纯 Go) | 更纯、无系统 git 依赖 |
| `globby` | `backend/local` 用 `doublestar` + gitignore 感知遍历 | 全语义在 backend |
| `mdast`(注释剥离) | [`goldmark`](https://github.com/yuin/goldmark) AST | 一并解决:注释剥离、指令定位、字面上下文豁免(正则方案的根本缺陷);AST 仅用于定位 span,输出仍字符串回填以逐字节保真 |
| `gray-matter` frontmatter | 内置 YAML 头剥离(轻量) | 仅取 `cwd`/`output` 作 Options |
| `fetch` + 全局缓存 | `IO.HTTP()`(调用方控制缓存) | 缓存是宿主职责 |
| —(新增) | START/END 标记 + dedup + `!`(§3) | token 节省;AI 可跨处关联 |
| —(新增) | 两命令契约:默认裸命令(doc)+ `resolve`(batch 数组,§4) | tool_call 粒度对接 |

---

## 8. TODO(明确不在本期)

1. **`@PLUGIN` 协议**:`[plugin-name:plugin-value](@PLUGIN)` 自定义协议,替代移除的 `jixo:` 内置符号。插件由调用方经 `Options` 注入解析器;core 仍纯。
2. **细粒度增量**:per-output dirty-tracking(`output → inputSet` 图,~150 行手写),仅当全量重跑成为瓶颈时引入。
3. **wasm 构建**:core + host-import backend(`IO` 由 wasm host 提供)。
4. **mindos VFS backend**:用 `vfs.Stat/ReadDir/Open` 实现 `IO`;`Git()` 返回 `ErrUnsupported`(@GIT_* 降级)。
5. **`@RUN` 指令**(注入终端命令输出,jixo2 提案):`[cmd](@RUN)`。

---

## 9. 验收与测试矩阵

| 层级 | 内容 |
|---|---|
| core 单测(无 IO) | grammar 解析、target/params 归一化、frontmatter/注释剥离、FILE/FILE_TREE 塑形、dedup + REF + `!`、MaxDepth 降级、未知 mode 降级、能力缺失降级 |
| backend 单测 | `local` 的 Glob(含 gitignore 各开关)、go-git 的 working/commit content/diff,用 tempdir + 临时 git 仓 |
| CLI 黄金快照 | 固定输入目录 → 固定输出;覆盖默认裸命令(doc)、`resolve`(batch 数组)、raw/packed |
| 契约测 | `resolve` JSON 数组 request/response 的序列化往返;退出码语义;数组内 dedup/REF 跨条目生效 |
| watch 测试 | tempdir 触发文件增删改 → 断言重跑产出更新(用可注入的短防抖) |
| 行为对齐 | 选取 jixo2 `gen-prompt.test.ts` / `gen-prompt.git.test.ts` 用例,迁移为 Go 用例,输出应等价(除新增标记外) |

**默认测试必须确定性**;涉及真实 git/网络的集成测为显式 gate。

---

## 10. 实现状态(Implementation Notes)

本节记录实现与 SPEC 早期措辞的偏差,均为澄清而非变更契约。

### 10.1 BDD 驱动(godog)

测试以 Gherkin `.feature` 文件驱动(`features/`),由 `github.com/cucumber/godog` 执行。step 定义在 `cli/` 包,通过 `cli.RunContext` 进程内调用 CLI(无子进程、无二进制)。当前覆盖:

| feature 文件 | 场景数 | 覆盖 |
|---|---|---|
| `cli_doc.feature` | 3 | doc 模式:文件展开、stdin、注释/链接豁免 |
| `resolve.feature` | 5 | resolve:单条/批量数组、packed gzip/zstd 往返、坏 JSON 退出码 |
| `dedup.feature` | 5 | 首次全量/重复 REF、`!` 强制全量、batch 跨条目 dedup、自递归/互递归终止 |
| `watch.feature` | 2 | 文件变更触发重跑、突发变更防抖合并 |

共 **15 场景**全绿 + core 单测。

### 10.2 递归与去重的统一(SPEC §2.3 / §3 的澄清)

早期 SPEC 草案把「递归遍历」和「输出去重」设想为两个独立阶段(traverse 全量注入 → 末尾 collapse 重复)。实现中发现这对**嵌套递归**有歧义(重复 id 出现在嵌套块内时,collapse 的行扫描无法正确配对 START/END)。

**最终语义(已实现):递归与去重在遍历层统一。**

- 首次见到某 content id → 发**全量**块,并继续展开其内容(递归)。
- 再次见到同一 id → 发 **REF**,且**不再展开**(天然终止所有确定性循环)。
- `!` → 在**隔离的 dedup 作用域**里发全量并递归。

**推论:dedup 终止所有确定性循环(自递归、互递归 a→b→a)。** `MaxDepth` 退化为纯安全网,仅当递归每次产生全新、未见过的内容时才可能触及——在无时间/状态的静态文件场景下不会发生。因此原设想的「max-depth 标记」在常规用法中不可达,测试改为验证 dedup 的终止行为(自递归→REF、互递归→REF)。

### 10.3 flag 顺序(`reorderFlags`)

Go 标准库 `flag` 在首个位置参数后停止解析 flag。`cli.reorderFlags` 把已知 flag 挪到位置参数之前,使用户可自然书写 `jixomd file.md --watch --base x`(无需 flag 在前)。

### 10.4 包结构(实际)

```
jixomd/
  grammar/      指令 AST 节点 + goldmark 自定义内联 parser(优先级 100 < link 200)
  core/         Expand 纯入口 + 遍历/去重/塑形(纯,零 IO)
  io/           IO / Git / HTTP 接口 + ErrUnsupported(纯,仅接口)
  contract/     Directive / Block + packed 编解码(gzip|zstd + base64)
  backend/
    local/      os + doublestar + gitignore 实现 IO(Git/HTTP = ErrUnsupported,TODO go-git)
  cli/          Run/RunContext + runDoc/runResolve + Watch + reorderFlags + signals
  cmd/jixomd/   main 薄壳
  features/     BDD 场景(.feature)
```

### 10.5 resolve batch 的去重共享(SPEC §4.3)

`resolve` 把整个 directive 数组**合成一份 Markdown**(用 `<!-- jixomd:BATCH:N:START/END -->` 哨兵包裹每条),跑**一次** `core.Expand`,再按哨兵切出每条 block。这样 dedup 作用域天然覆盖整个数组(跨条目),无需 core 暴露单独的 batch 入口。

### 10.6 尚未实现(对应 §8 TODO,本期脚手架返回注释)

- `@FILE_TREE` / `@FILE_LIST` / `@GIT_FILE` / `@GIT_DIFF` → `<!-- jixomd: mode X not implemented -->`。
- `@PLUGIN` 协议。
- 细粒度增量 watch(当前为粗粒度全量重跑,对齐 jixo2)。
- mindos VFS backend / wasm 构建。
