# 响应出口：Send / finish

状态：已实现（2026-11-09，工作区，基线 `56fca2e`）。记录时间 2026-11-07。

## 目标

统一 handler 的响应出口，避免 `SendString` / `SendBytes` / `SendJSON` / `SendStatus` 全家桶，同时支持链式写法：

```go
c.Status(http.StatusCreated).Send(mux.JSON(v))
```

## 职责划分

- **Send 只负责写**：选格式、设 `Content-Type`、写 body；不记录日志、不碰错误漏斗。
- **finish 只负责收尾**：提交预设状态、记一条访问日志。
- **writeError 仍是唯一错误写出口**：错误策略由各出口先决定（路由过 `ErrorHandler`、NoMatch 直接写、panic 走中止语义），finish 只做与出口无关的收尾。

日志不能放进 Send：NoMatch、规范化重定向、panic 这三条出口不经过 Send；一次请求也可能写多次；日志要的是最终状态码、字节数与耗时。收尾也不塞进 `handlePanic`：中止语义要求不写头，commit 会破坏它。

## 已定方案：A（Renderer，去掉 ContentType()）

```go
type Renderer interface {
    // Render 先设置 Content-Type 再写 body；失败时不得修改响应头。
    Render(c *Ctx) error
}

func (c *Ctx) Send(r Renderer) error // Render 后提交预设状态
func JSON(v any) Renderer            // marshal 成功后才设 Content-Type
```

选 A 不选 B：B 每加一种格式都要改 `Send` 的 switch；A 的调用形态正是目标，且用户可自定义 Renderer（模板、XML）。接口只有一个方法，复杂度是挣来的。起步只提供 `JSON`，其他格式出现真实调用再加（YAGNI）。

## 原未决问题的结论

1. Send 不带 status 参数；状态只走 `c.Status(...)` 预设，避免两套来源。
2. 删除 `ContentType()`；renderer 在 `Render` 里设头。JSON 先 marshal，成功后才设头，失败时响应头保持干净。
3. `c.Status(201)` 后直接写 body：现有 writer 已按预设状态提交（`WriteString → track → commit`），不需要“只对 Send 生效”的文档，也不需要 panic。
4. 错误不走 Send：Send 返回 error，handler 返回它；`ErrorHandler` / `writeError` 仍是唯一错误漏斗。
5. Flush 先提交预设状态；Hijack 后不写头，未写状态时 `StatusCode()` 为 0。
6. 测试见下。

## 当前代码位置

- `send.go`：`Renderer`、`Send`、`JSON`。
- `ctx.go`：`finish`（commit + logAccess）。
- `router.go`：路由闭包、NoMatch 回调、ServeHTTP 兜底统一调 `finish`。
- `send_test.go`：JSON、预设状态提交、marshal 失败、自定义 Renderer。
- 既有测试：`TestPresetStatusCommitsOnWrite`、`TestCommitPromotesInformationalStatus`、`TestWriterSwitchingProtocolsIsFinal`。

## 不做

- 不提供 `SendString` / `SendBytes` / `SendStatus`。
- 不做响应缓冲；链式成立依赖“预设 + 提交点”，不是缓冲。
