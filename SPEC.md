# jixomd SPEC

> 状态:**v1 已实现全部 6 种 MODE**(见 §10)· 实现语言:Go · 从 jixo2 `gen-prompt` 迁移而来的**独立** Markdown 扩展引擎

---

## 0. 愿景与定位

### 0.1 它是什么

jixomd 是一个**纯 Markdown 扩展引擎**:读入含指令的 Markdown,把指令(`[src/**](@FILE)`)就地展开为真实内容(文件、git diff 等),输出完整的 Markdown。

### 0.2 它解决什么问题

Agent / LLM 的 prompt 经常需要「把项目里的真实内容拼进来」——文件源码、目录树、git diff。传统做法是让 AI 在运行时**动态调工具**去取(`read_mind` / `query_context` / …),每次 round-trip 一次。jixomd 把这件事**前置成声明式的 prompt 预处理**:作者在 Markdown 里写 `[src/**](@FILE)`,引擎在 prompt 进入处理队列前就把内容拼好。结果是「等价于 AI 自己调工具取了内容」,但零 round-trip、可缓存、可 diff。

### 0.3 核心洞见:纯粹性来自架构切分,而非语言特性

> 「纯粹」不是靠「在进程内用接口隔离 IO」实现的,而是靠「把解析(PARSE)和合并(MERGE)留在服务端、把执行(EXECUTION)整个赶到客户端」实现的。

```
┌─ 服务端(概念:Router / Expert 处理队列前的 prompt 预处理)──────────────┐
│  ① DETECT:扫到 jixomd 语法 → ② DELEGATE:整份 md 或指令数组下发       │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼  tool_call
┌─ 客户端(jixomd 二进制,持有真实 repo)──────────────────────────────────┐
│  ③ 完整引擎:PARSE → RESOLVE(读真 FS / git) → MERGE → FORMAT         │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼  tool_call_result(回灌)
┌─ 服务端 ───────────────────────────────────────────────────────────────┐
│  ④ SPLICE / 替换 → 完整 prompt → Expert 开始处理                       │
└────────────────────────────────────────────────────────────────────────┘
```

- 服务端**永远不碰**文件 / git / 网络——它只做字符串变换和编排。
- 所有副作用都在客户端那个 `jixomd` 二进制里。
- 这比「进程内用 `Io` 参数 threading」(Zig `std.Io` 的模型)是**更强的隔离**:物理拆成两个进程,边界是一份可序列化契约。
- 因此**语言选 Go**(而非 Zig):纯粹性已由架构保证,不需要 `std.Io`;且唯一宿主生态是 Go(mindos 等),原生 import 零摩擦。

### 0.4 三种用法,一份核心

| 用法 | 形态 | 典型场景 |
|---|---|---|
| **独立 CLI** | `jixomd file.md`(doc 模式,raw) | 本地开发,替代 jixo2 的 `jixo G`;含 `--watch` |
| **tool_call 集成** | `jixomd resolve`(batch JSON 数组) | agent pipeline:服务端 detect → delegate → 客户端 resolve → 回灌 splice |
| **Go 库** | `import "jixomd/core"` | 嵌入任意 Go 程序,复用 parser / merger / core |

三种用法共享**同一个 core**(纯函数 `Expand`)和同一套 START/END 标记。

### 0.5 独立且不绑定宿主

- 可单独打包成一个二进制;也可只复用其中某个部件(parser、merger、local backend)。
- **不绑定任何具体宿主**。本文档不含 memoryai / mindos 等下游系统的内容——它们只是「换一个 `IO` backend」的特例(见 §2.2、§8 TODO)。

### 0.6 设计原则(贯穿全文)

- **SRP**:core(纯变换) / backend(IO) / cli(装配与 watch)三层职责单一。
- **DIP**:core 依赖 `IO` 抽象,不依赖 `os`/`git`/`net` 具体实现。
- **KISS / YAGNI**:对齐 jixo2 的**粗粒度**响应式(任意输入变更 → 全量重跑),不引入细粒度增量框架;`jixo:` 内置符号移除,改为 `@PLUGIN` 协议并推迟到 TODO。
- **正则 → AST**:指令识别基于标准 Markdown AST(goldmark),而非正则——这是相对 jixo2 的根本正确性修复(代码块 / 注释里的 `[..](@..)` 不再误匹配)。

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
| `@GIT_DIFF` | git 视角的 **diff**(工作区/index vs HEAD/base、ref range、或指定 commit vs parent) | `{path, diff, status}` | diff 围栏块,标题带 `(status)` |

未知 MODE → 输出 `<!-- jixomd: unknown mode <MODE> -->`(不报错,容错降级)。

### 1.3 target 三态

1. **glob / 路径**:`src/**/*.ts`、`a/b/c`、`` `a/b/c` ``(反引号包裹则去边界,用于含特殊字符)。
2. **URL**:`https://...` / `http://...` → 经 `IO.HTTP()` 拉取文本。
3. **`commit:path`(仅 git 类 MODE)**:`HEAD:**`、`abc123:src/*.js`。首个 `:` 之前为 git ref,之后为文件 pattern,多个 pattern 以逗号分隔。
   - **`plugin:val`(`@PLUGIN` 协议)**:**TODO,本期不实现**。见 §8。

> 注:原 jixo2 的内置符号(`jixo:coder`/`jixo:pwd`/`jixo:datetime`/`jixo:memory`)**全部移除**,统一由 `@PLUGIN` 协议(可插拔)替代。

#### 1.3.1 路径解析规则(Path resolution)

相对 target 的**基准目录**遵循以下规则(在 core 的 `rebaseTarget` 中实现,顺序即优先级):

1. **`$VAR` / `${VAR}` 展开**先于一切。`$PWD` 特殊处理 → 解析为 **BaseDir**(cwd / `--base`),**不读进程环境**,保证同一份文档在不同 shell 下行为一致。其它变量名回退到 `os.Getenv`。
2. **`pwd:` scheme**:`target` 以 `pwd:` 开头时,剥去前缀,余下部分相对于 **BaseDir**(cwd / `--base`)解析。这是「强制走项目根 / cwd」的标准写法。
3. **文档相对(默认)**:其余相对 target 相对于 **DocDir**(源 `.md` 文件所在目录)解析,对齐 Markdown/HTML 的惯例,使文档跨 cwd 可移植。
4. **绝对路径**原样透传。

> 例:在 `proj/sub/doc.md` 中,`[../sib.md](@FILE)` 指 `proj/sib.md`(文档的兄弟),而 `[pwd:top.md](@FILE)` 与 `[$PWD/top.md](@FILE)` 指 cwd 下的 `top.md`。
>
> 实现注:rebase 后的路径**不**做 `filepath.Clean`(保留 `..`),后端据此选择合理的遍历根(首个 `..` 之前的字面目录),再对 pattern 与候选路径统一 normalize 后匹配。`BaseDir == ""` 且 `DocDir == ""` 时(如内存测试)target 原样透传,以兼容 flat-key 的 fake 文件系统。
>
> CLI:`doc` 模式下 `local.New(DocDir)`(后端遍历根 = 文档目录,输出路径相对文档可读);`--watch` 下每个 input 各自 `DocDir = dirname(input)`、`BaseDir = cwd/--base`,因此 `[../*.md](@FILE)` 在两种模式下含义一致。

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
| `staged` | bool | git 类:仅 staged(`@GIT_*` 工作区视角);与 `@GIT_DIFF?base=<ref>` 组合时表示 index vs base |
| `base` | string | `@GIT_DIFF` 工作区/index 比较基准;默认 `HEAD`,如 `[src/**](@GIT_DIFF?base=main)` |
| `compare` | string | `@GIT_DIFF` ref range,格式 `left..right`,如 `[src/**](@GIT_DIFF?compare=main..feature)`;不支持 `...` merge-base 语义 |
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
│ cli           doc(默认裸命令)/ resolve / --watch       │
│               (装配 + watch 循环 + fsnotify + 防抖)     │
└───────────────────────┬─────────────────────────────────┘
                        ▼ 依赖
┌─────────────────────────────────────────────────────────┐
│ backend  local(os + doublestar + gitignore + go-git)    │
│          (实现 IO 接口;Git 可用,HTTP 返回 ErrUnsupported) │
│          [未来: wasm host-import / mindos VFS]           │
└───────────────────────┬─────────────────────────────────┘
                        ▼ 依赖
┌─────────────────────────────────────────────────────────┐
│ contract    Directive / Block + packed 编解码(纯)       │
└───────────────────────┬─────────────────────────────────┘
                        ▼ 依赖
┌─────────────────────────────────────────────────────────┐
│ core (纯)  Expand: Parse / Resolve / Merge / Format     │
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
    BaseChangedFiles(base string, staged bool) ([]GitFile, error)      // base -> worktree/index
    BaseDiff(base, path string, staged bool) (string, GitStatus, error)
    RangeChangedFiles(left, right string) ([]GitFile, error)           // left -> right
    RangeDiff(left, right, path string) (string, GitStatus, error)
    FilesAtCommit(ref string, globs []string) ([]string, error)
    CommitContent(ref, path string) (string, GitStatus, error)         // git show ref:path
    CommitDiff(ref, path string) (string, GitStatus, error)            // 该 commit 引入的 diff(vs parent)
}

type HTTP interface {
    Get(url string) (body []byte, contentType string, err error)
}
```

- `IO.Glob` 的**全语义**(pattern 匹配 + 目录遍历 + gitignore)由 backend 实现;core 不自行遍历文件系统(保持纯)。
- **watch 模式**(见 §5)当前为**粗粒度**:cli 递归监听整个 `BaseDir`,任意变更触发全量重跑(对齐 jixo2 `reactive-fs`)。**未来可选优化**:backend 在实现 `IO` 时记录每次访问(Stat/ReadFile/Glob 的入参与扫描根),供 watch 层缩小监听面——采集逻辑在 backend 而非 core,core 不感知 watch。当前未实现。

### 2.3 递归与收敛

- `@INJECT` 一个 `.md` 文件、或 URL/资产返回含指令的内容时,**该内容被重新 PARSE** 并继续展开(服务端/客户端同此规则)。
- **递归与去重在遍历层统一**(见 §10.2 的澄清):首次见到某 content id → 发全量块并继续递归;再次见到同一 id → 发 REF 且不再展开。这天然终止所有**确定性循环**(自递归、互递归 a→b→a)。
- `MaxDepth`(默认 8)是**纯安全网**:仅当递归每次产生全新、未见过的内容时才可能触及——在无时间/状态的静态文件场景下不会发生。超限 → 该处输出 `<!-- jixomd: max depth exceeded -->`,不 panic。

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

- `id` = 内容稳定哈希(对 `@FILE`/`@GIT_FILE` 取 `mode+path(+ref/working source)`;对 `@GIT_DIFF` 取 `mode+path+有效比较源`;对 `@INJECT` 取内容 hash)。同一 `id` 即同一份内容。
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
- **watch 根 + `.gitignore`**:默认对 `Options.BaseDir` 递归监听,**遵守 `.gitignore`(根 + 嵌套,git 语义),并始终跳过 `.git`**。这避免了把 `node_modules`/`.git`/`dist` 等成千上万个目录加入 fsnotify 监听——在 macOS(kqueue,每目录 1 FD)上会迅速耗尽 FD 配额,导致每次防抖重跑的 `os.ReadFile` 报 `too many open files`、`.gen.md` 不再更新。
  - 启动时打印一行汇总,如:`jixomd: watching 116 directories under . (skipped 4858 ignored by .gitignore, most by rule "target/")`。被跳过数 = 被忽略子树内**全部**目录(含根),主导规则按命中数取最大。
  - 实时 `Create` 事件(新建目录)也走同一套 ignore 判定,避免运行期新生成的 `node_modules/...` 重新泄漏。
- **路径基准**:`--watch` 下每个 input 的 `DocDir = dirname(input)`、`BaseDir = cwd/--base`,因此相对 target 的解析与 `doc` 模式一致(见 §1.3.1)。
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
6. **URL 注入(`IO.HTTP`)**:`[https://...](@FILE)` 当前返回 unsupported 注释;实现 `net/http` backend + `mime_<type>_lang` 映射。
7. **`@FILE_TREE` 的 `expandDirectories` / 更完整的 glob 选项**:当前 `renderFileTree` 总是全展开;`expandDirectories=false` 时折叠目录为单节点。

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
| `modes.feature` | 6 | FILE_LIST / FILE_TREE、lang / map_ext / prefix / noFound 塑形 |
| `git.feature` | 12 | @GIT_FILE(工作区内容+状态)、@GIT_DIFF、base/range/staged 比较、git ignore 过滤、删除文件、未改动文件 |
| `regressions.feature` | 5 | review 回归:params、空 batch、dedup、ignore、commit glob |
| `path-resolution.feature` | 5 | 文档相对、`..`、`pwd:`、`$PWD`、绝对路径 |
| `watch-gitignore.feature` | 3 | watch 遵守 `.gitignore` 且跳过 ignored subtree |
| `watch-multi.feature` | 2 | 多输入 watch、生成物排除 |

共 **48 场景**全绿 + core 单测。

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
    local/      os + doublestar + gitignore + go-git 实现 IO(Git 可用,HTTP = ErrUnsupported)
  cli/          Run/RunContext + runDoc/runResolve + Watch + reorderFlags + signals
  cmd/jixomd/   main 薄壳
  features/     BDD 场景(.feature)
```

### 10.5 resolve batch 的去重共享(SPEC §4.3)

`resolve` 把整个 directive 数组**合成一份 Markdown**(用 `<!-- jixomd:BATCH:N:START/END -->` 哨兵包裹每条),跑**一次** `core.Expand`,再按哨兵切出每条 block。这样 dedup 作用域天然覆盖整个数组(跨条目),无需 core 暴露单独的 batch 入口。

### 10.6 已全部实现(本期)

§1.2 的全部 6 种 MODE 已实现并通过 BDD 验证:

| MODE | 实现 | backend |
|---|---|---|
| `@INJECT` | core(纯) | IO.ReadFile |
| `@FILE` | core(纯,栅栏包裹) | IO.ReadFile |
| `@FILE_LIST` | core(纯,路径逐行) | IO.Glob |
| `@FILE_TREE` | core(纯,├──/└── 树形) | IO.Glob |
| `@GIT_FILE` | core + IO.Git | go-git(working + commit) |
| `@GIT_DIFF` | core + IO.Git(内置 unified diff;支持 HEAD/base/range/commit-parent) | go-git |

§1.4 输出塑形 params 已实现:`lang` / `ext` / `map_ext_<ext>_lang` / `prefix` / `filepath` / `noFound[.msg/.prefix/.suffix]`。glob 控制 params 已实现:`gitignore` / `ignore` / `ignoreFiles` / `dot`。

### 10.7 npm 分发(optionalDependencies 模式)

npm 包采用 **per-platform optionalDependencies** 分发(esbuild / swc / @biomejs 同款模式):

- 主包 `jixomd`(unscoped):声明 6 个 `optionalDependencies`(`@jixo/md-{os}-{arch}`),npm 根据 `os`/`cpu` 字段自动只安装匹配平台的那一个。
- 6 个平台子包:`@jixo/md-darwin-arm64`、`@jixo/md-darwin-x64`、`@jixo/md-linux-arm64`、`@jixo/md-linux-x64`、`@jixo/md-win-arm64`、`@jixo/md-win-x64`,各自含一个原生二进制。
- pnpm workspace(`pnpm-workspace.yaml`)管理版本:源码期子包间用 `workspace:*` 引用;发布前由 `release.yml` / `scripts/publish.js` 临时重写为真实 semver,避免把 workspace 协议泄漏到 registry 包。
- `index.js` 的 `binaryPath()` 解析顺序:`JIXOMD_BINARY_PATH`(开发覆盖)→ `require.resolve('@jixo/md-{slug}/jixomd')`(已安装的 optionalDep)→ 本地 workspace fallback。
- GitHub Actions(`release.yml`):tag `v*` 触发 6 矩阵交叉编译(CGO disabled、`-trimpath -ldflags="-s -w"`)→ 编译进各自 `npm/jixomd-{slug}/` 目录 → 逐包 `npm publish --provenance --access public` 发布全部 7 个包。已存在的 exact version 会跳过,其它 publish 错误会失败。GitHub Release 同时创建,作为直接下载的 fallback。
