# 响应出口：Send / Renderer

状态：草案，未实现。记录时间 2026-11-07，代码基线 `1df1b14`。

## 目标

统一 handler 的响应出口，避免 `SendString` / `SendBytes` / `SendJSON` / `SendStatus` 全家桶，同时支持链式写法：

```go
c.Status(http.StatusCreated).Send(mux.JSON(v))
```

## 已确定的约束

- `Ctx.Status(code int)` 只**预设**最终状态码，返回 `*Ctx`，本身不写响应。
- 1xx（101 除外）立即交给 `WriteHeader`，不进入预设；101 是最终状态。
- 预设状态由 `commit()` 提交：首次写 body、Flush、ReadFrom，或路由成功返回时。
- 非法状态码（<100 或 >999）立即 panic。
- 错误出口保持单一漏斗：handler 返回 `error` → `Config.ErrorHandler`；不新增 `SendError`。
- 不使用 `(data, format enum)` 之外的第二种“全家桶” API。

## 待定方案

### A. Renderer 接口

```go
type Renderer interface {
    ContentType() string
    Render(c *Ctx) error
}

func (c *Ctx) Send(r Renderer) error
// 或显式带状态：func (c *Ctx) Send(status int, r Renderer) error
```

格式构造器放包级函数，例如 `mux.JSON`、`mux.String`、`mux.Bytes`。

- 优点：格式可扩展；Content-Type 与 body 分离，符合“先设头、再写 body”的时序。
- 缺点：多一个接口；用户目前不接受 `ContentType()`。

### B. 单入口枚举

```go
c.Send(data, mux.FmtJSON)
```

- 优点：调用短。
- 缺点：格式固定，新增格式要改枚举；Content-Type 仍需在写头前确定。

## 未决问题

1. Send 是否带 `status` 参数，还是完全依赖 `c.Status(...)` 链式。
2. `ContentType()` 是否保留；若不保留，采用 `Send(contentType, renderer)`、renderer 自写 header，还是预渲染成 `[]byte`。
3. `c.Status(...)` 之后直接 `c.W.WriteString(...)` 的语义：底层会按 200 写头。是文档写死“Status 只对 Send 生效”，还是检测到直写就 panic。
4. 错误出口是否也统一走 `c.SendXX`（当前倾向：不统一，保留 `ErrorHandler`）。
5. Flush / Hijack 与预设状态的交互边界。
6. 相关测试的重写：`Status` 预设后由 Send/链尾提交的行为断言。

## 参考库

- gin：`c.Render(code, render.Render)`，格式是 `render` 包里的独立类型。
- fiber：`c.Format(body)` 按 `Accept` 协商；fasthttp 的 Response 是内存对象，可延迟发送。
- go-restful：`WriteHeaderAndEntity(status, value)`，按 MIME 注册表协商。
- 本项目直接包装 net/http，没有响应缓冲；链式成立依赖“预设 + 提交点”，不是缓冲。

## 当前代码位置

- `ctx.go`：`Status` 预设最终码并返回 `*Ctx`；1xx 走 `SetStatus` → `WriteHeader`。
- `write.go`：`SetStatus`、`commit`、`WriteHeader`；101 视为最终状态。
- `router.go`：handler 成功返回后调用 `c.W.commit()`，提交未写出的预设状态。
- 测试：`TestPresetStatusCommitsOnWrite`、`TestCommitPromotesInformationalStatus`、`TestWriterSwitchingProtocolsIsFinal`。

## 下一步

先选 A 或 B，再实现 `Send` 与对应测试；Content-Type 的时序问题必须在写代码前定下来。
