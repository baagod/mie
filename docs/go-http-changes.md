# Go 1.22–1.27.4 net/http 变更

状态：参考资料，记录 1.22 到 1.27.4 之间 `net/http`（另含少量 `net/url`）的可见变更。

核实口径：符号与行为逐条对照本机 go1.27.4 源码（`$(go env GOROOT)/src/net/http`），行号指向该版本；**版本归属**依据各版发布说明，源码本身不带版本标记。补丁级安全修复无法离线核实，单列在 §7。

## 1. Go 1.22

- `ServeMux` 支持方法、主机、通配段：`"GET /users/{id}"`、`"/files/{path...}"`、`"/{$}"`。注册冲突在启动期 panic；路径按段反转义（`%2F` 不再被整体还原）；`GODEBUG=httpmuxgo121=1` 回退旧行为。
- `Request.PathValue` / `SetPathValue`（`request.go:1495`）配合上面的模式取值。
- 文件服务加了 FS 版本：`ServeFileFS`、`FileServerFS`、`NewFileTransportFS`（`fs.go:856` 起）。
- 空 `Content-Length:` 头从「忽略」变为「报错」；`GODEBUG=httplaxcontentlength=1` 恢复宽松解析（`transfer.go:1088`）。

## 2. Go 1.23

- `Request.Pattern`（`request.go:332`）记录命中的 ServeMux 模式，未命中为空串。
- Cookie 解析独立成函数：`ParseCookie`（`cookie.go:91`）、`ParseSetCookie`、`Request.CookiesNamed`（`request.go:439`）；`Cookie` 增加 `Quoted`、`Partitioned`。
- `ServeContent`/`ServeFile`/`ServeFileFS` 出错时清掉 `Cache-Control`、`Content-Encoding`、`Etag`、`Last-Modified` 四个响应头（`fs.go:190` 的 `serveError`），避免成功响应的缓存头漏到错误响应；`GODEBUG=httpservecontentkeepheaders=1` 恢复旧行为。

## 3. Go 1.24

- 协议选择：`Protocols` 值类型（`http.go:29`）+ `SetHTTP1`/`SetHTTP2`/`SetUnencryptedHTTP2`；挂在 `Server.Protocols` 与 `Transport.Protocols`。`SetUnencryptedHTTP2` 即 h2c，同一端口可同时服务 HTTP/1 与 h2c。
- HTTP/2 调参：`HTTP2Config`（`http.go:249`）+ `Server.HTTP2`、`Transport.HTTP2`，字段含 `MaxConcurrentStreams`、`PingTimeout`、`WriteByteTimeout` 等。此前这些只能通过 `x/net/http2` 设置。

## 4. Go 1.25

- `CrossOriginProtection`（`csrf.go:36`）：内置 CSRF 校验。
  - `Check(req)` 顺序：`GET`/`HEAD`/`OPTIONS` 直接放行 → `Sec-Fetch-Site` 为 `same-origin`/`none` 放行、为空转下一步、其余值拒绝（exempt 例外）→ `Origin` 为空放行 → `Origin` 的 host 等于 `req.Host` 放行 → 否则拒绝。
  - `Handler(h)` 拒绝时自己写 403；`Check` 不调用 `SetDenyHandler` 的钩子，只返回 error。
  - `AddTrustedOrigin` 要求带 scheme 和 host，不接受 path/query/fragment；`AddInsecureBypassPattern` 用 ServeMux 语法，只匹配直达请求。
  - 比较的是 host，不含 scheme：HTTP 站点收到 HTTPS 同主机名的 `Origin` 也放行。

## 5. Go 1.26

- `Transport.NewClientConn(ctx, scheme, address)`（`clientconn.go:113`）返回显式管理的 `ClientConn`：`Reserve`、`Release`、`Available`、`InFlight`、`RoundTrip`、`SetStateHook`、`Close`、`Err`。用于自建连接池或手工复用连接。
- `HTTP2Config.StrictMaxConcurrentRequests`（`http.go:266`）：严格限制并发流数量。
- 解析上限：`net/http` 的 cookie 个数上限 3000（`cookie.go:71` 的 `defaultCookieMaxNum`，`GODEBUG=httpcookiemaxnum=N`）；`net/url` 的 query 参数个数上限 10000（`url.go:961` 的 `defaultMaxParams`，`GODEBUG=urlmaxqueryparams=N`，0 表示不限制）；`urlstrictcolons` 控制裸冒号 URL 的解析。

## 6. Go 1.27

- `Server.MaxHeaderValueCount`（`server.go:3133`）+ `DefaultMaxHeaderValueCount = 500`（`server.go:940`）。计的是**值个数**：单行逗号分隔算 1 个，多行各算 1 个；`Trailer` 例外，其逗号分隔值逐个计数（因为后续会各走一行）。
- `Server.DisableClientPriority`（`server.go:3197`）：HTTP/2 默认按 RFC 9218 处理客户端优先级，置 true 退回 round-robin。仅在 HTTP/2 且未自定义写调度器时生效。
- 客户端 `Response.Body` 被 `Close()` 后异步排空未读数据，上限 256 KiB（`transport.go:2420` 的 `maxPostCloseReadBytes`），便于连接复用。

## 7. 补丁版本

以下行为在当前源码里可核实，但具体从哪个补丁版本引入需对照发布说明：

- `Range` 请求头最多处理 200 段（`fs.go:1025` 的 `defaultMaxContentRanges`），`GODEBUG=httpservecontentmaxranges=N` 调整，`0` 表示不限制。
- `MaxBytesReader` 超限后服务端会关闭连接，不再复用（`server.go:1751` 检查 `requestBodyLimitHit`）。1.27.4 是安全修复版，本条与超限后的写头时序相关；完整 CVE 清单以 go.dev 发布说明为准，本文件不转述编号。

