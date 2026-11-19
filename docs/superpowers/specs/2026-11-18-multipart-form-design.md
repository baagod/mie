# Ctx multipart/form-data 绑定 — 设计决策记录

> 状态：**已定稿**（D1–D4 全部有结论）；实施计划见 [2026-11-18-multipart-form-binding.md](../plans/2026-11-18-multipart-form-binding.md)
> 记录方式：用户每答一条，立刻在本文件追加一条决策；不等全部问完再汇总。
> 批准后由 `writing-plans` 依据本文生成实施计划。

## 1. 意图与 why

`Ctx.Bind` 与 `Ctx.Form[T]` 现在只认 `application/x-www-form-urlencoded` 与 query。`multipart/form-data`（浏览器原生表单提交、带或不带文件）目前既绑不上值，也不报错——静默零值。目标是把表单值这条路补齐，让「同名字段 query 覆盖 form」的既有语义在 multipart 下也成立。

## 2. 事实基线（已核实，不是决策）

- `net/http` 的 `ParseMultipartForm` 会把 multipart 的值同时写进 `r.Form` 与 `r.PostForm`（`request.go:1421-1423`，注释 "r.PostForm should also be populated. See Issue 9305"）。因此 `bind` 的 `form` 源无需改动，只需换解析入口。
- `ParseMultipartForm` 的 `maxMemory` 只约束内存，超出部分溢出到磁盘临时文件，磁盘无上限；标准库要求调用方事后 `MultipartForm.RemoveAll()`。
- `maxMemory` **不是请求体上限**，只决定内容放内存还是写盘；标准库内部的默认值是未导出的 `defaultMaxMemory = 32 << 20`，且 `ParseMultipartForm(0)` 不会回落到该默认值（0 表示文件全部写盘），所以必须显式传一个正数，数值取 `32 << 20` 与标准库自身行为一致。
- `ParseMultipartForm` 对非 multipart 请求返回 `ErrNotMultipart`，**不能**无条件替换现有的 `ParseForm` 调用。
- `bind` 唯一解析入口：`internal/bind/decoder.go:44` 的 `r.ParseForm()`，由 `internal/bind/plan.go:36` 的 `plan.hasParams` 控制。
- `Ctx.Form` 与 `Ctx.Bind` 共用 `bind.FormCache`：query 与 body 各解析一次（`internal/bind/cache.go`），`Ctx.Form` 不再直接调 `ParseForm`。
- `BindBody` 对 form 与 multipart 均早退（`ctx.go:260`），不读 body。
- `MaxBodyBytes` 当前明确跳过 multipart（`app.go:39-40` 注释、`app.go:88` 的 `ct != MIMEMultipartForm` 守卫），注释写「上传大小由上传路由自行限制」。

## 3. 目标 / 非目标

- 目标：`Bind` 能绑定 `multipart/form-data` 的表单值，且「同名字段 query 覆盖 form」的既有语义保持成立；`*multipart.FileHeader` / `[]*multipart.FileHeader` 字段可直接绑定（D1）。
- 非目标：multipart 的 body 上限（D2：明确不设）、独立的文件上传公开方法（`Ctx.FormFile` 之类；文件走 `Bind` 字段或 `Ctx.Form` 的文件类型）。

## 4. 决策

| # | 问题 | 结论 | 理由 |
|---|---|---|---|
| D1 | 文件部分是否进公开 API | **走 D**：复用 `Bind`，字段类型写 `*multipart.FileHeader` / `[]*multipart.FileHeader`，不新增公开方法；文件只从 `form` tag 绑定，写到其它 tag 的声明被忽略（不报错，便于同一 IN 复用到多种来源） | 用户：struct 里写了文件字段就是想要文件；只绑值而丢弃文件等于字段失效，用户还得自己解析一遍。m01190 追加：非 form tag 报错会让 DTO 无法复用，改忽略 |
| D2 | 字节上限归谁管 | 保持现状：multipart 继续豁免 `MaxBodyBytes`，`maxMemory` 用常量，不新增配置项 | 用户：普通 body 1MiB 够用，但 multipart 不能设上限；`BindBody` 与 multipart 是两类请求，此前的豁免是故意的 |
| D3 | 解析失败语义 | `Bind` 遇 multipart 解析失败（缺 boundary、part 损坏、读 body 出错）→ 400，与 `bind` 现有其它解析错误一致；另：`form` 字段声明为非文件类型但请求携带同名文件 → 400（避免静默零值）；`Ctx.Form` 解析失败返回零值（D5），不走 400 | 用户：「错误保持一致」；静默失真比报错更贵 |
| D4 | `Ctx.Form` 是否同步支持 | 本次不改，只动 `bind` | 用户：「先不管 Ctx.Form」。代价：`Bind` 能取 multipart、`Form` 取不到（已知且接受的不对称） |
| D5 | 反转 D4：`Ctx.Form` 支持 multipart | 值与文件都支持：`T` 为 `*multipart.FileHeader` / `[]*multipart.FileHeader` 时取文件 part，其余类型取 `PostForm`；query 命中仍短路不读 body；解析失败返回零值 | 解析收成 `bind.ParseForm` 唯一入口后，`Form` 与 `Bind` 共用同一段解析，旧的不对称没有保留理由；用户决定实施 |
| D6 | 解析缓存 | `bind.FormCache` 持 query 与 form 两张表，各解析一次；`Decode` 遍历到 form 源时由 `bindForm` 逐字段懒取，`Ctx.Form` / `Ctx.Bind` 共用；解析错误只存一个 `err`，由 `bindForm` 取值时当场返回；已有错误时两张表都返回空表、不再解析 | 每次 `Ctx.Form` 都重跑 `url.ParseQuery` / `ParseForm` 是重复解析；两张表分开才能保住 URL 命中不读 body（Q4）与只绑 path 不读 body（D2） |
| D7 | 删除 query tag | 对外只保留 `form`：先 URL 后 body，URL 命中不读 body；删除 `query` 来源与 `bindQuery`。代价：纯 URL 字段缺值时也会解析 body，body 可补 URL 缺值 | 两套 tag 要分别维护解析路径；用户要求统一成一个入口（m01144） |

## 5. 第 1 轮 frontier（待答）

### Q1 只解析值，还是连文件一起给 API（仍是唯一待答项，且决定实现形态）

multipart 请求里有两种数据：**值**（`<input name="name">` 的 `name=张三`，落在 `MultipartForm.Value`）和**文件**（`<input type=file>`，落在 `MultipartForm.File`）。Q1 问的是这次改动做到哪一步。三个形态：

**A. `ParseMultipartForm(maxMemory)` 一次解析**（约 3 行）
调一次标准库，值会同时进 `r.PostForm`（`request.go:1421-1423`），`bind` 的 `form` 源直接可用；文件进 `r.MultipartForm.File`。代价：文件内容被摊开（≤maxMemory 在内存，超出写磁盘临时文件），框架负责 `RemoveAll()`。

**B.（已否决）`r.MultipartReader()` 流式跳过文件**（约 25-30 行）
否决理由：`r.Body` 只能读一次，被跳过的 part 不可恢复。一旦本次跳过文件，将来要支持文件或 `Ctx.Form` 就必须重写解析层，且无法补读——用「读的时候决定不要」换「以后返工」，不划算。

**D. A + 把 `*multipart.FileHeader` 也当可绑定字段**（Gin 风格，约 +25 行）
```go
type Upload struct {
    Title  string                `form:"title"`
    Avatar *multipart.FileHeader `form:"avatar"`
}
```
`bind` 在字段循环里对这两个类型开专用通道，从 `r.MultipartForm.File` 取值；缺失给 `nil`，支持同名多文件。不多一个公开方法。

**结论：D**。解析层用 `ParseMultipartForm`（无损），文件字段直接可绑；单独的 A 不采纳（理由见第 6 节）。清理责任：框架在 handler 返回后调 `MultipartForm.RemoveAll()`，handler 执行期间 `FileHeader.Open()` 始终可用。


### Q3（已定 → D3）

`Bind` 遇 multipart 解析失败统一返回 400，与 `bind` 现有其它解析错误一致。用户原话：「错误保持一致」。

### 已定（转入第 4 节）

- Q2 → D2：保持现状，multipart 继续豁免 `MaxBodyBytes`，不新增配置项。
- Q4 → D4：`Ctx.Form` 本次不改，只动 `bind`。

## 6. 已否决的方案（含理由）

- **只解析值、丢弃文件（A）**：struct 里写了文件字段却永远拿不到，等于绑定不完整；用户只能绕过 `Bind` 自己再解析一次 body，与「struct 里声明了就该填上」直接冲突。

## 7. 留给实施者的部分

访谈未定、但实现时必须落地的三处（实施者按此执行，不必再问）：

1. **解析分支**：`internal/bind/decoder.go:44` 从 `r.ParseForm()` 改为按 Content-Type 分支——multipart 用 `r.ParseMultipartForm(multipartMemory)`，其它维持 `ParseForm()`。`multipartMemory` 用常量（建议 `32 << 20`），不新增配置项（D2）。
2. **清理挂点**：`RemoveAll()` 必须挂在请求级 `defer`（panic 路径也要执行），不能挂在 `Bind` 内部——`Bind` 返回后 handler 仍要 `FileHeader.Open()`。
3. **文档**：`docs/design-handler.md` §5 与 D5 同步——`Bind` 与 `Ctx.Form` 都能取 multipart（值 + 文件）；`Ctx.Form` 解析失败返回零值而不是 400。

已知风险（D2 的直接后果，用户已接受）：multipart 不受 `MaxBodyBytes` 约束，`ParseMultipartForm` 会把超大文件写到磁盘临时文件，构成磁盘耗尽面；缓解只能靠部署层（反代/nginx）限制请求体。
