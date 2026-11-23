# Changelog

mie 的用户可见变更记录。格式参考 [Keep a Changelog](https://keepachangelog.com/)；已发布的条目只追加，不重写。

## [Unreleased]

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

- **不做 OpenAPI**，这是框架之外的关注点。
