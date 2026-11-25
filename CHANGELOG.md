# Changelog

mie 的用户可见变更记录。格式参考 [Keep a Changelog](https://keepachangelog.com/)；已发布的条目只追加，不重写。

## [Unreleased]

### Changed

**`WithContext` 的 `C` 改为编译期约束。** 签名从 `WithContext[C any]` 变为 `WithContext[C mieCtxCarrier]`（未导出接口）。值嵌入 `Ctx`、命名字段包装、`C = Ctx` 以前是注册期 panic，现在是编译错误；指针上下文（如 `**Ctx`）能过约束，但 `C` 不是结构体，仍在注册期 panic。通过指针链嵌入的写法不受影响。`factory == nil` 同样在注册期 panic；两处共用消息 `mie: WithContext requires a non-nil factory and a struct context`。

**请求上下文改为复用。** `Ctx` 与 `ResponseWriter` 从 `sync.Pool` 取用，请求结束（含 panic）时清零并放回。处理器不得把 `*Ctx` 或其派生引用带出请求生命周期；跨请求持有会读到后续请求的状态。

**`Ctx.Log` 不再有包级兜底。** 包级 `logger` 变量删除，默认 Logger 在 `New` 里就地构造（`TextHandler(os.Stderr)`，仍开在 `LevelTrace`），零值配置的输出不变。没有 App 的 `Ctx` 调 `Log`，从原来的静默写 stderr 改为 nil deref panic。

**路由派发改由注册进 `ServeMux` 的入口交回。** 注册期把处理器链装进注册给 `ServeMux` 的闭包，它被调用（即命中）时把链写到 writer，`App.ServeHTTP` 据此派发。`Ctx` 因此不再塞进 request context：`Ctx.R` 就是原请求本身，`Request.WithContext` 与 `context.WithValue` 的两笔分配消失（空 GET 465B/8 次 → 81B/5 次）。`ServeMux` 亲自处理的 404/405 与规范化重定向不经过这个入口，仍走原路径。已知限制：`GODEBUG=httpmuxgo121=1` 下旧 mux 不解析「方法 + 路径」写法，全站路由命中不了——这在此改动之前就成立，不是本次引入。

### Fixed

**嵌套值嵌入不再静默拷贝上下文。** `type Inner struct{ Ctx }` + `type Outer struct{ *Inner }` 这类写法仍能通过编译，但工厂返回的 `*Outer` 持有的是 `Ctx` 的拷贝。现在首次用到上下文时会校验 `mieCtx()` 是否指回本次请求的 `*Ctx`，不等（包括未填 `Ctx` 字段的 nil 指针）就 panic `mie: WithContext factory returned nil or a *C with wrong *Ctx`，不再让链状态静默分叉。

## [0.3.0] - 2026-11-25

### Added

`WithContext` 接受跨层指针嵌入。 以前只有直接嵌入 `*Ctx` 的结构体能注册，`type App struct{ Base }`（`Base` 内嵌 `*Ctx`）会在注册期 panic，现在这种写法可用。注册期改为检查值 `C` 是否实现了私有接口 `mieCtxCarrier`：指针嵌入把 `*Ctx` 的 `mieCtx()` 提升到值 `C`，值嵌入只提升到 `*C`，两种形态仍能区分。

### Changed

**`WithContext` 的校验从 “扫描顶层匿名字段” 改为 “值类型方法集”。**

### Known issues

跨层嵌入值 `Ctx`，注册仍会通过。例如 `type Inner struct{ Ctx }`、`type Outer struct{ *Inner }`：`Outer` 从 `*Inner` 拿到提升的 `mieCtx()`，注册成功，但它持有的是 `Ctx` 的拷贝。框架只校验最外层；这种写法要求每一层都用指针嵌入。

## [0.2.0] - 2026-11-25

**破坏性变更：响应格式入口移到 `render` 包。** 0.1.0 的 `mie.JSON`、`mie.XML`、`mie.Formatted`、`mie.Media` 与 `Codecs.Order []Media` 不再存在；升级时按下面逐项替换：

- `mie.JSON(v)` → `render.JSON(v)`
- `mie.XML(v)` → `render.XML(v)`
- `mie.Formatted` → `render.Formatted`（自定义格式实现该接口）
- `mie.Media` / `mie.MediaJSON` 等 → `render.MediaJSON` 等常量；`Codecs.Order` 改为 `[]string`

### Changed

**`Codecs.Order` 接受 `[]string`，未知媒体在 `New` 时 panic。** 以前只有 `Media` 常量能赋给 `Order`，拼写错误在编译期被挡住；现在任何字符串都能写进去。`New` 解析时精确匹配媒体名，拼错或带参数（如 `application/json; charset=utf-8`）会 panic，已知但未实现的媒体仍被跳过。

### Removed

**`mie` 不再导出格式入口。** `JSON`、`XML`、`Formatted` 与 `Media` 移到新包 `render`；`mie.MIME*` 保留为 `render.Media*` 的别名，值不变。

## [0.1.0] - 2026-11-24

**API 未冻结。** 0.1.0 是首个公开版本，破坏性改动进入 minor 版本（0.2.0、0.3.0）。

### Added

- **路由与生命周期。** `New` 返回 `*App` 并实现 `http.ServeHTTP`；`Run(addr, certFiles...)` 按证书数量启动 HTTP 或 HTTPS，`RunListen` 交给调用方自己的 listener；`Config.ServerConfig` 在 `http.Server` 建好后调整它。路由模式沿用 `net/http` 的 `{name}` 语法，`c.Param`、`c.Path`、`c.Pattern` 取  当前请求信息。
- **类型化 handler。** `HandlerFunc[IN, OUT]` 声明输入与输出，`Get`/`Post`/`Put`/`Patch`/`Delete`/`Handle` 从实参推断 IN 与 OUT；`He` 适配“只返回 error”的写法。输出经 `Send` 按 `Accept` 协商编码。
- **应用上下文。** `Router.WithContext[C]` 返回 `TypedRouter[*C]`，`TypedHandler[*C, IN, OUT]` 在处理器签名里直接拿到你的上下文；工厂 `func(*Ctx) *C` 在首次用到时构造，同一请求只构造一次，`Group` 继承同一工厂。`Use[P, IN]` 同时接受 `func(*Ctx, IN)` 与 `func(*C, IN)` 两种中间件，其余形状在注册期 panic。
- **请求绑定。** `c.Bind[IN]` 按 `path`、`header`、`cookie`、`form` 标签取值（query 与表单共用 `form`），body 按 Content-Type 用 `json`/`xml` 解码；支持嵌套结构、切片、数组、指针与 `TextUnmarshaler`，同一请求内按 IN 类型缓存；`BindBody` 只解 body；`c.Form[T]` 直接取表单值。`**T` 在注册期拒绝，其余非法输入在绑定时报错并交给错误处理器。
- **multipart 表单。** `Ctx.Form` 同时覆盖普通值与文件；文件切片只复制结构，绑定结果不会反向修改表单；解析内存上限 32 MiB，上传体积由调用方用 `http.MaxBytesReader` 控制。
- **响应与状态。** `Status`、`Header`、`Type`、`StatusCode` 可链式设置；`Send` 对 101/204/304 跳过 body；非 multipart 请求的 body 默认上限 1 MiB，超出返回 413。
- **错误模型。** `Error` 携带状态码与文本，`NewError` 与覆盖 net/http 全部状态码的 `StatusXXX` 构造器可直接返回；`Config.ErrorHandler` 接管用户链返回的错误，默认写出 `text/plain` 与 `nosniff`；响应已提交时只追加错误文本，并记一条 warn。
- **日志与恢复。** 访问日志与框架日志统一走 `slog`（`Config.Logger`），默认在 `LevelTrace` 可见；panic 恢复输出带调用帧的 JSON，依赖包的帧被过滤。
- **零依赖。** 只使用标准库。

### Changed

- **取消请求不再被记成 200。** 只有 `context.Canceled` 改写为 499，其他错误保留真实状态码，避免故障在指标里变成成功。
- **注册期校验替代运行期失败。** IN 冲突、IN 与路由不匹配、nil 中间件、nil 处理器都在注册期 panic，不再等到首个请求才 500。
- **`Pattern` 不再带方法前缀，`TraceID` 不再采纳客户端传入的头。**
- **2xx 响应携带 error 时记错误日志**，不再静默丢弃。

### Fixed

- **停机超时不再当干净退出。** 超时后强制断开连接并返回错误，而不是报告成功。
- **Hijack 之后不转发最终头。** 连接被接管后不再调用 `WriteHeader`，错误处理也不会谎报状态码。
- **空 body 不再被当成非法 JSON。** chunked 的空 body 按零值处理。
- **同名参数不再互相踩。** URL 命中同名值时不再读 body；文件字段只认文件 part，忽略同名 URL 参数。

### Deliberate omissions

**不做 OpenAPI**，这是框架之外的关注点。
