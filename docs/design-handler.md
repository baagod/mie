# Handler 设计

状态：已按 2026-11-11 讨论定稿。基线 `fe6a5a5`。
本文只定义接口形状与语义，不含实现。

## 1. 目标

处理器（handler）是「输入 → 输出」的纯函数，路由负责把 HTTP 请求变成输入、把输出变成 HTTP 响应。

- 类型化：每个路由自己声明输入和输出类型，编译期可检查。
- 默认处理器必须带输入：`func(c *Ctx, in IN) (OUT, error)`（`HandlerFunc[IN, OUT]`）。
- 没有输入的路由传 `struct{}`，不另设无输入处理器；没有输出的场景用 `He` 适配。
- 位置来源只认显式 tag：没写 path/header/query/form tag 的字段不会被位置来源写入；body 仍按 `encoding/json` 标准规则解码，未打 tag 的导出字段也会写入。
- 外部处理器一律用 `HandlerFunc[IN, OUT]` 注册；框架适配成内部的 `Handler`（`func(c *Ctx) error`）后入链，中间件也是 `HandlerFunc`。

非目标：

- 不做自动 OpenAPI/schema 生成。
- 不做反射路由、不做 `any` 形状的默认处理器。
- 不做 `c.JSON` 那类手动挂载响应的旧模型（输入绑定走 `Ctx.Bind[IN]`，见 §4）。

## 2. 处理器形状

处理器分两层：

```go
// 注册口：外部处理器与中间件都用它，类型在注册时推断。
type HandlerFunc[IN, OUT any] func(c *Ctx, in IN) (OUT, error)

// 运行口：框架内部适配后的链元素，只接收 *Ctx。
type Handler func(c *Ctx) error
```

`IN` / `OUT` 是 `HandlerFunc` 的类型参数，不能省略 `IN`。`IN` 可以是 struct、`*struct`，或能整体 JSON 解码的非 struct 类型（见 4.2）；`OUT` 同理，通常用值。

```go
// 无输入：显式传 struct{}
app.Get("/health", func(c *Ctx, _ struct{}) (Health, error) {
    return Health{OK: true}, nil
})

// 有输入、有输出
app.Post("/users", func(c *Ctx, in CreateUser) (User, error) {
    return svc.Create(c, in)
})
```

适配处理器（第一版只有一个）：

```go
// He：只返回 error 的处理器，OUT 固定为 nil（不写 body）。
func He[IN any](h func(c *Ctx, in IN) error) HandlerFunc[IN, any]
```

```go
app.Delete("/users/{id}", He(func(c *Ctx, in DeleteUser) error {
    return svc.Delete(c, in.ID)
}))
```

约定：

- 处理器与适配器的结果统一是 `HandlerFunc[IN, OUT]`；注册时框架再把它适配成 `Handler`，绑定与渲染逻辑只有一份。
- 每个处理器都声明 IN；没有输入就写 `struct{}`。路由的适配器只按“有没有输出”区分，因此只需要 `He`。
- 处理器返回非 nil error 时忽略 OUT。
- OUT 的接口零值（`nil`）不写 body；其它值都参与编码（`encoding/json/v2`）：`struct{}` → `{}`，`nil` 指针 → `null`，`nil` 切片 → `[]`，`nil` map → `{}`，`0` → `0`。
- 中间件是 `HandlerFunc[IN, any]`：必须调用 `c.Next()`，返回 `nil` 表示不写 body。

## 3. 注册 API

沿用现有路由方法，增加类型参数：

```go
func (r *Router) Get[IN, OUT any](pattern string, h HandlerFunc[IN, OUT])
func (r *Router) Post[IN, OUT any](pattern string, h HandlerFunc[IN, OUT])
// Put/Patch/Delete 同型；Use 返回 error（typed 中间件校验）

// 手写响应/非泛型入口。
func (r *Router) Handle(method, pattern string, h Handler)
```

- 类型参数从处理器字面量推断（Go 1.27 支持泛型方法与字面量推断，已验证）；`He(...)` 内联也能推断，无需显式实例化。
- 推断失败时显式实例化：`r.Get[CreateUser, User]("/users", h)`。
- 注册时把 `HandlerFunc` 适配成链元素 `Handler` 存进路由表；IN/OUT 在注册点擦除，`OUT` 是否写 body 由返回值决定（`nil` 不写），`IN` 的绑定由 `Ctx.Bind` 在请求中完成（每请求一次）。
- 同一字段可声明多个来源 tag：按 `path` → `header` → `query`/`form`（参数）→ body 的顺序依次绑定，后写的覆盖先写的；某个来源没提供值时跳过、不覆盖；不 panic（tag 可能另有用途，如序列化或别的库）。
- `query` 与 `form` 是同一个来源：都从 `ParseForm` 后的参数表取值，不再区分 URL query 与表单 body。
- body 不按字段声明，整块交给 `encoding/json` 解码：未打 tag 的导出字段也会被写入，`json:"-"` 不写，键名大小写敏感（标准库默认精确匹配）；只有路径/头部/参数来源才要求显式 tag。
- 未导出字段（无论有没有 tag）一律跳过：反射写不进去，留给 handler 私下用；空 tag（如 `path:""`）等同没写。
- 非法声明不 panic、注册也不报错：类型非法在首次绑定时才暴露，按绑定失败返回 400（含原因文案）。
  - path/header/参数指向的类型不是标量（string/bool/整数/浮点，含其指针；或元素为标量的切片/数组）。
  - 不同字段可以声明同一个来源 + key：各写各的字段，拿到同一个值。

## 4. 输入绑定（IN）

绑定发生在「第一个声明 IN 的元素」处（叶子处理器，或未来的 typed 中间件），只绑定一次。

位置/头部/参数来源只认显式 tag；body 的字段匹配交给 `encoding/json` 标准规则。

实现见 `bind/plan.go` 与 `../bind/scalar.go`：`bind.Bind` 扫描字段并写入、`bindValue` 取来源值、`setValues` 写字段。方案按 IN 的元素类型缓存（`sync.Map`），同一类型只扫描一次；非法类型的错误也随方案缓存。

入口是 `Ctx.Bind[IN any](in *IN) error`：第一次绑定位置来源与 body 后把 IN 存进请求上下文，后续调用直接取缓存，不再重复绑定。

### 4.1 字段 tag

```go
type UpdateUser struct {
    ID    int    `path:"id"`
    Page  int    `query:"page"` // 与 form 同义
    Trace string `header:"X-Trace-Id"`
    Name  string `json:"name"`  // 只影响 body 解码（标准库）
    note  string // 无 tag：位置来源不写；未导出字段 body 也不写
}
```

| 字段声明 | 来源 |
|---|---|
| `path:"id"` | 路径参数 `id`（`c.Param("id")`） |
| `header:"X-Trace-Id"` | 请求头 `X-Trace-Id` |
| `query:"page"` / `form:"page"` | 参数 `page`：`ParseForm` 后的 `c.R.Form`（GET 为 URL query；POST/PUT/PATCH 的 urlencoded body 与 query 合并，body 优先） |
| 不写位置 tag | 位置来源不写；body 仍可能写入（`encoding/json` 规则） |
| `json:"-"` | body 不写；位置来源若无其它 tag 也不写 |
| 多个位置 tag | 依次 `path` → `header` → `query`/`form` 绑定，后写覆盖先写；来源缺失（没有这个参数/头）跳过，来源存在但值为空串（`?x=`、空 header）则把字段清零 |
| 位置 tag + body | 位置来源先绑，body 最后整块解码覆盖（非 GET/HEAD 且 Content-Type 为 JSON 时；body 里没有的键保留位置来源的值） |
| 切片/数组字段 | 来源提供多个值时写入全部值：切片全量、数组按序填充（超出丢弃、不足补零）；标量字段只取第一个 |

- 多值来源：参数重复（`?id=1&id=2`）或同名 header 多个值时，切片写入全部值、数组按声明长度依次填充；标量字段只取第一个值。首个值为空串（`?x=`、空 header）仍按空值清零。

- 参数来源：IN 里有参数字段时调用一次 `c.R.ParseForm()`；`ParseForm` 自己决定是否读 body（POST/PUT/PATCH 且 urlencoded 时读并合并），框架不还原 `c.R.Body`，需要原始 body 的调用方自己提前 `c.Body()` 或重新序列化。
- body 来源：当方法不是 GET/HEAD 且 `Content-Type` 的 media type 是 `application/json` 时，读取 `c.Body()` 并整块 `json.Unmarshal(b, in.Addr())`；CT 缺失或无法解析（畸形）视为没有 JSON CT，不读 body、不报错：
  - 不设内置上限，body 全量读入内存；应用中间件可用 `http.MaxBytesReader` 收紧，超限由 `Ctx.Body()` 映射为 413；
  - 空 body 跳过解码、保持零值；`null` 解码为零值；数组等与 struct 不匹配时由解码错误返回 400；
  - 字段匹配、未知键、大小写、`json` tag 选项全部按 `encoding/json` 标准规则；
  - 解码失败为 bind 错误（400），文案含 `bind body`。
- `struct{}` 不绑、不读 body（框架短路）。
- v1 限制：参数/头部只扫顶层字段，不展开匿名嵌入（body 交给标准库，嵌入按标准库处理）。

### 4.2 `IN` 与 `*IN`、允许的类型

- `IN` 允许 struct、`*struct`、`struct{}`，以及其它能整体 JSON 解码的类型（`map[string]any`、切片、标量）。
- 非 struct 的 `IN` 不做 tag 绑定：整个 body 就是值（不设内置上限）；仅当方法不是 GET/HEAD 且 `Content-Type` 的 media type 是 `application/json` 时解码，其它/缺失/畸形时不读 body、`IN` 为零值；空 body 时也为零值。
- `*T` 由框架分配，handler 通常拿到非 nil 指针；例外是 body 为 JSON `null` 时解码会把指针置回 nil，handler 需自行判空。指针归请求所有，handler 不应持有到请求之外。
- 拷贝是**浅拷贝**：值 `IN` 被复制时，结构体里的 slice / map / 指针 / 接口字段仍共享底层数据，改这些字段的元素或目标会反映到原对象；`i := *in` 取副本再改，也只隔离顶层值字段。要完全隔离只能深拷贝。
- 语义选择：只读输入用值 `IN`；需要跨链共享修改用 `*IN`；只想局部改值字段、不影响原对象可用 `i := *in`。
- 传值成本：一次 struct 拷贝，大小按字段量算；对常见 DTO（几十到几百字节）远小于 JSON 解码的微秒级开销。第一版只在叶子绑定一次，因此只拷贝一次。
- 适合 `*T` 的时机：DTO 很大（多 KB）、未来 typed 中间件沿链传 IN（每层一次拷贝）、或需要让下游看到字段修改。
- `*T` 的代价是绑定多一次堆分配，同样可忽略。默认建议用值。

## 5. 输出渲染（OUT）

| 情形 | 响应 |
|---|---|
| error 非 nil | 忽略 OUT，走错误出口 |
| `OUT` 为 nil（接口零值） | 不写 body；状态取 `c.Status` 预设，未设置则 200 |
| `OUT` 为 `struct{}` | 编码为 `{}`；`Content-Type: application/json`；状态取预设，未设置则 200 |
| 其它类型 | JSON 编码；`Content-Type: application/json`；状态取预设，未设置则 200 |
| JSON 编码失败 | 500，交错误出口 |

零值语义：接口零值不写 body；其余值都编码（`encoding/json/v2`）——`struct{}` → `{}`，`nil` 指针 → `null`，`nil` 切片 → `[]`，`nil` map → `{}`，`0`、`""` 照常写出。

保持既有约定：`c.Status(...)` 只记录状态，真正写出发生在响应提交时；空 body 的状态由收尾提交。

## 6. 错误路径

- 处理器返回 error → `Config.ErrorHandler` → 非 nil 时 `writeError`。
- **bind 错误同样交给 `Config.ErrorHandler`**（用户可在这里定制 400 文案/状态）。
- panic → `Config.RecoverHandler`，行为与现在一致。
- 所有错误最终仍由 `writeError` 写响应（唯一错误出口）。

bind 错误的形态：位置来源与 body 解码错误统一为 `*Error`（默认 400）；body 读取错误原样返回——`http.MaxBytesReader` 超限由 `Ctx.Body()` 映射为 `*Error` 413，其余读取错误（连接中断等）按普通错误走 500。文案含来源、字段与原因，便于 `ErrorHandler` 直接取用。`ErrorHandler` 仍按现有契约返回 error，最终由 `writeError` 写响应；要改状态码或文案就在这里返回自己的 `*Error`。类型非法不做预处理，同样在绑定时暴露，按绑定失败 400 返回。

## 7. 中间件与链

中间件是 `HandlerFunc[IN, any]`，返回 `nil` 表示没有输出：

```go
func (r *Router) Use[IN any](m HandlerFunc[IN, any]) error
func (c *Ctx) Next() error
```

```go
r.Use(func(c *Ctx, _ struct{}) (any, error) {
    log.Println("before")
    err := c.Next()
    log.Println("after")
    return nil, err
})
```

- 只返回 error 的中间件用 `He` 适配：`r.Use(He(func(c *Ctx, _ struct{}) error { ... }))`（OUT 为 `nil`，不写 body）。
- v1 只接受 `IN = struct{}`；带 IN 的中间件（typed middleware）由 `Use` 返回 error，见 7.1。
- 中间件必须调用 `c.Next()` 推进链；返回 `nil` 不写 body，返回非 nil 值则由框架照常编码（调用方自己负责）。
- 处理器和中间件共用同一条链，链元素统一是 `Handler`（`func(c *Ctx) error`）；`Next()` 不再携带 `any` 输入，也不再返回 `any`。
- 无输入处理器也显式写 `_ struct{}`：`r.Get("/health", He(func(c *Ctx, _ struct{}) error { ... }))`；有输出的无输入处理器写 `func(c *Ctx, _ struct{}) (OUT, error)`。

### 7.1 typed 中间件（第一版不做）

先完成泛型处理器；typed 中间件放到后续版本再评估。届时需要满足的约束：

- 整条链只有一个 IN；typed 中间件与叶子的 IN 必须一致，否则注册即失败（优先编译期检查）。
- 绑定在第一个 typed 元素处执行（lazy），之前的无类型中间件先运行；解析失败的错误从该元素返回，走 `ErrorHandler`。
- 原始 body 需要可重放：所有 body 读取走框架入口，首次读取时缓冲并替换 `c.R.Body`；直接读 `c.R.Body` 的代码不保证后续可重放。
- 代价：body 缓冲（失去流式）、每链 lazy 状态（unbound/bound/error）、typed 中间件值不能跨不同 IN 复用。

该扩展不改变默认行为：只做透传的中间件（IN 为 `struct{}`）、未使用 typed 中间件的路由，绑定仍在叶子。

## 8. 与现有实现的关系

- `Ctx.finish` 仍是收尾出口：`writeError` + `commit` + `logAccess`。
- `writeError` 仍是唯一错误写出口；`ErrorHandler`/`RecoverHandler` 契约不变。
- 路由闭包：绑定 → 调处理器 → 渲染；错误交给 `ErrorHandler`，最终由 `finish` 收尾。
- 链元素统一为 `Handler`（`func(c *Ctx) error`）；路由注册口收 `HandlerFunc[IN, OUT]`，中间件注册口收 `HandlerFunc[IN, any]`。

## 9. 已定与延后

已定：

1. `OUT` 的接口零值（`nil`）不写 body；其它值都编码（`struct{}` → `{}`）。中间件返回 `nil` 即无输出。需要完全手写响应时用 `Handle`。
2. `He` 把只返回 error 的处理器适配成 `HandlerFunc[IN, any]`（`OUT` 为 `nil`，不写 body），命名与推断可用（`r.Delete("/users/{id}", He(func(c *Ctx, in DeleteUser) error {...}))` 免显式类型参数，已验证）；无输入处理器不另设形状，直接在 IN 处写 `struct{}`。v1 只做 `He`。
3. bind 错误：位置来源与 body 解码错误为 `*Error`（默认 400）；body 读取超限（`*http.MaxBytesError`）映射为 413，其余读取错误走 500。文案含来源/字段/原因；`ErrorHandler` 可改变最终响应。
4. 来源顺序：同一字段多个来源 tag 按 `path` → `header` → `query`/`form`（参数）→ body 依次绑定，后写的覆盖先写的；来源缺失（没有该参数/头）跳过，来源存在但值为空串则清空字段（不 panic）。body 在非 GET/HEAD 且 CT 为 JSON 时读取，整块交给 `encoding/json`：未知键忽略、键名大小写敏感（标准库默认精确匹配）、未打 tag 的导出字段也会写入。框架不设 body 上限，需要限制时由应用中间件用 `http.MaxBytesReader`，超限映射为 413。
5. `Next()` 从 `(any, error)` 改为 `error`，是一次性破坏性变更，已随类型化 Handler 提交合入。
6. `IN` 类型范围：struct / `*struct` / `struct{}` / 可整体 JSON 解码的非 struct。
7. `query` 与 `form` 是同一个参数来源：都读 `ParseForm` 后的 `c.R.Form`；GET/HEAD 只有 URL query，POST/PUT/PATCH 的 urlencoded body 与 query 合并，body 优先。`ParseForm` 会消耗 body，框架不还原。
8. 外部处理器一律 `HandlerFunc[IN, OUT]`，框架适配成内部 `Handler`（`func(c *Ctx) error`）入链；中间件用 `HandlerFunc[IN, any]` 注册，返回 `nil` 表示无输出。
9. 绑定在请求中由 `Ctx.Bind` 完成：同一请求只绑一次并缓存结果；`Get/Post/Put/Patch/Delete` 不返回 error，`Use` 仍返回 typed 中间件的校验错误。

延后：

- typed 中间件（见 7.1）。
- body 重放与流式、严格未知字段模式。
- `multipart/form-data`（v1 不解析）。

## 10. 测试计划

- 处理器形状：有输入/无输入（`struct{}`）、有输出、只返回 error（`He`，不写 body）、预设状态、手写响应（`Handle`）；中间件用 `HandlerFunc[IN, any]` 或 `He(...)` 注册，返回 `nil` 不额外写 body。
- 绑定：path/header/参数（query 与 urlencoded form 合并）各来源、类型转换失败（文案含实际 tag，如 `query.Page`）、同字段多来源按 path → header → params → body 顺序绑定、后写覆盖、缺值跳过、未导出字段跳过（不 panic）、空 tag 等同没写、空 body、非法 JSON、body 非对象（数组/标量）400、body `null` 零值、未知键忽略、未打 tag 的导出字段按标准库绑定、键名大小写敏感（标准库默认精确匹配）、中间件 `http.MaxBytesReader` 超限 413、body 在非 GET/HEAD 且 CT 为 JSON 时读取（CT 非 JSON 含缺失/畸形时不读，忽略大小写与 `; charset` 参数）、显式空值（`?x=`、空 header）清空字段、来源缺失跳过、非 struct IN 整体解码、切片/数组字段接多值（切片全量、数组按序填充/超出丢弃/不足补零、标量只取第一个）。
- body 缓存：`Ctx.Body()` 重复调用返回同一份字节与错误；中间件先读后叶子绑定仍能读到。
- 指针 IN：非 nil、各来源正常绑定、生命周期不跨请求。
- 输出：JSON 编码失败、nil 输出不写 body、零值照常编码、`struct{}` 输出、预设 204。
- 错误：handler error、bind error 均进 `ErrorHandler`；panic 进 `RecoverHandler`。
- 中间件：无类型中间件顺序不变；typed 中间件（若实现）绑定时机与错误归属。
- 绑定错误：注册不报错；类型非法在请求时返回 400（含原因文案）；并发首批请求安全。
