# Handler 设计

状态：与当前实现同步（2026-11-14）。本文只定义形状与规则，不含实现。

## 1. 目标

- 类型化：路由声明输入/输出类型，编译期可检查。
- 默认处理器 `func(c *Ctx, in IN) (OUT, error)`；无输入写 `struct{}`；无输出用 `He`。
- 位置来源只认显式 tag；body 交给 Content-Type 对应的解码器（默认 JSON）。
- 外部处理器统一适配成 `Handler`（`func(c *Ctx) error`）后入链。

非目标：自动 OpenAPI/schema、反射路由、`any` 形状的默认处理器、`c.JSON` 手动挂载响应的旧模型。

## 2. 处理器形状

```go
type HandlerFunc[IN, OUT any] func(c *Ctx, in IN) (OUT, error)
type Handler func(c *Ctx) error
func He[IN any](h func(c *Ctx, in IN) error) HandlerFunc[IN, any]
```

- `IN` / `OUT` 是类型参数，不能省略 `IN`；`IN` 支持 struct、`*struct`、可整体解码的非 struct。
- 返回值：error 非 nil 时忽略 OUT；OUT 接口零值不写 body；`struct{}` 编码为 `{}`，其余用 `encoding/json/v2` 编码。
- 中间件是 `HandlerFunc[IN, any]`：必须调用 `c.Next()`，返回 nil 表示不写 body。
- 处理器与中间件最终都适配成 `Handler`（`func(c *Ctx) error`）入链。

## 3. 注册 API

```go
func (r *Router) Get[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router
func (r *Router) Post[IN, OUT any](pattern string, h HandlerFunc[IN, OUT]) *Router
// Put/Patch/Delete 同型；Use 返回所在组，可链式注册
func (r *Router) Handle[IN, OUT any](method, pattern string, h HandlerFunc[IN, OUT]) *Router
func (r *Router) Group(prefix string) *Router
func (r *Router) Use[IN any](m HandlerFunc[IN, any]) *Router
```

- 注册方法返回所在组，可链式追加；`*App` 嵌入根 `Router`，`app.Get` 等价于在根组注册。
- `Group` 的前缀按 `path.Join` 归一，pattern 末尾的 `/` 保留（ServeMux 靠它表示子树）；前缀缺前导 `/` 时补上，`""` 与 `"/"` 都不加前缀。pattern 为空时落在前缀本身，前缀末尾的 `/` 同样保留（`/api` 精确、`/api/` 子树）。
- 组的中间件是创建时的快照：父组之后再 `Use` 不影响已建组，子组 `Use` 不影响兄弟。
- 组不能 `Run`：服务生命周期只在 `App` 上。

- 类型参数从处理器字面量推断，失败时显式写 `Get[CreateUser, User]`。
- 注册只把处理器适配成链元素；中间件声明的 `IN` 在注册时与本层路由的 `IN` 比对，不一致 panic。
- 绑定失败返回 400（含来源、字段与原因）。

## 4. 输入绑定（IN）

绑定发生在第一个声明 IN 的元素处（v1 是叶子处理器），一个请求只绑一次并缓存。

- 位置来源只认显式 tag：`path:"id"`、`header:"X-Trace-Id"`、`query:"page"` / `form:"page"`（query 与 form 是同一个参数来源）。没写 tag 的字段不会被位置来源写入。
- 顺序：`path` → `header` → `query`/`form` → body，后写覆盖先写；来源缺失跳过，来源存在但值为空串则清零字段。
- 未导出字段、空 tag 一律跳过；非标量（指针/切片/数组取元素后仍非标量）在首次绑定时报 400。
- 多值来源：切片写全部、数组按序填充（超出丢弃、不足补零）、标量只取第一个。
- 参数来源调用一次 `ParseForm`；它决定是否读 urlencoded body，框架不还原 `c.R.Body`。
- body：非 GET/HEAD 且 `Content-Type` 有已注册解码器时，读 `c.Body()` 解码（默认只注册 `application/json`，应用可注册其它）；CT 缺失/畸形/未注册时不读、不报错。空 body 跳过；无内置上限，超限由 `Ctx.Body()` 映射 413；解码失败 400。
- `struct{}` 不绑不读 body；`*T` 由框架分配，handler 通常拿到非 nil（body 为 `null` 例外）；IN 是浅拷贝。
- 参数/头部只扫顶层字段，不展开匿名嵌入；body 的嵌入按解码器规则处理。

## 5. 输出渲染（OUT）

- handler 返回非 nil error：忽略 OUT，走错误路径。
- OUT 接口零值：不写 body；状态取 `c.Status`，未设置则 200。
- 其它值：JSON 编码（`encoding/json/v2`），`struct{}` → `{}`；`nil` 指针 → `null`、`nil` 切片 → `[]`、`nil` map → `{}`。
- `c.Status` 只记录状态，真正写出在响应提交时。

## 6. 错误路径

- handler error、bind error 都交给 `Config.ErrorHandler`，最终由 `writeError` 写响应（唯一错误出口）。
- bind 错误：来源绑定与解码失败为 `*Error`（400）；`http.MaxBytesReader` 超限由 `Ctx.Body()` 映射 413；其它读取错误（连接中断等）按 500。
- panic 走 `Config.RecoverHandler`。

## 7. 中间件与链

- 中间件是 `HandlerFunc[IN, any]`，用 `Use` 注册，必须调用 `c.Next()`；返回 nil 不写 body，返回其它值由框架照常编码。
- 只返回 error 的中间件用 `He` 适配：`He(func(c *Ctx, _ struct{}) error { ... })`。
- 链元素统一是 `Handler`（`func(c *Ctx) error`），`Next()` 只返回 error。
- `Use` 追加到当前层，只覆盖之后注册的路由；已注册的路由不受影响。
- 中间件声明的 `IN` 必须与该链最终处理器的 `IN` 一致：注册时比对，不一致 panic。

## 8. 已定与延后

已定：OUT 零值不写 body；`He` 是唯一适配器；bind 错误 400/413/500 分流；来源顺序 path → header → param → body、后写覆盖、空值清零；`Next()` 只返回 error；`IN` 支持 struct / `*struct` / `struct{}` / 可整体解码的非 struct；body 按 Content-Type 查解码器（默认 JSON）；处理器统一 `HandlerFunc[IN, OUT]` 入链，中间件收 `HandlerFunc[IN, any]`；中间件的 `IN` 须与最终处理器一致，注册期校验。

延后：body 重放/流式；严格未知字段；`multipart/form-data`。

## 9. 测试要点

- 处理器形状：有/无输入、有输出、`He`、`Handle`。
- 绑定：各来源与顺序覆盖、缺值/空值、未导出字段、非法类型 400、多值切片/数组、body 各 CT（含未注册）、body 读取错误 413/500、缓存与并发首批请求。
- 输出：nil、`struct{}`、编码失败、预设状态。
- 错误：`ErrorHandler` / `RecoverHandler`；中间件 `Next` 顺序。
