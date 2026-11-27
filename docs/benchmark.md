# 基准记录

状态：一次性测量记录，不是性能保证。改动热路径后重跑并更新本文件。

## 1. 测量条件

- 时间：2026-11-28（第十一次复测）；Windows 11；Intel i7-8700K；Go 1.27.4；**GOMAXPROCS=2**。
- 命令：`GOMAXPROCS=2 go test -run '^$' -bench . -benchmem -count=10`，取中位数（每个变体 10 个样本，附 95% 置信区间）。
- 为什么钉线程数：12 线程时同机重复跑会漂 ±13–19%（机器被其它任务占用时），固定 2 线程后降到 ±5–8%；对照组 huma / fursy 的中位数不受线程数影响（差 1–3%），只有 mie 在 2 线程下快 3–5%（GC 后台 worker 不抢核）。
- 写入器：`nopWriter`（复用 header map，避免 `httptest.ResponseRecorder` 的分配）；不含网络、TLS 与真实编码器差异。
- 本表三列全部由仓库外 `../benchcmp` 在同一进程、同一 harness 里跑出（`replace github.com/baagod/mie => ../mux`），跨库倍率只在这张表内成立。仓库根目录的 `bench_test.go` 跑同样的 mie 两档，请求构造与 writer 不同，绝对数不能跨表比，只用于本仓库的回归跟踪。
- 三列都关闭访问日志：`mie` 用 `slog.New(slog.DiscardHandler)`（`Enabled` 恒 false，框架零日志开销），huma 与 fursy 本来就不内置访问日志。mie 开箱配置（`TextHandler(io.Discard)` 开在 `LevelTrace`，日志真格式化一遍再丢字节、不付 `write(2)`）只在 `bench_test.go` 里跟踪，不进对比表——见 §5。
- 路由地板：同一进程里给每个路由库注册单条路由 + 一次 `WriteString`，不含绑定与 JSON，用来把「路由层」从「框架层」里减出来。
- 三个场景，payload 相同（8 字段 JSON，约 120 字节）：
  1. `GET /hello`，`IN = struct{}`，只测路由 + 收尾。
  2. `POST /json`，JSON body 解到 8 字段 struct。
  3. `POST /full?page=3&sort=name` + `X-Tag` + `Cookie: sid=abc`，同时覆盖 body、query、header、cookie。
- 对照组：huma v2.40.0（humago 适配 `http.ServeMux`）、fursy v0.7.0（自研 radix）。跨库对照代码在仓库外 `../benchcmp`，避免把参照库依赖带进本仓库。

## 2. 结果

单位：ns/op，括号内为 B/op 与 allocs/op。

| 场景 | mie | huma | fursy |
|---|---|---|---|
| 空 GET | 538（33 / 3） | 1425（280 / 9） | 406（42 / 4） |
| JSON body | 2379（657 / 10） | 2072（593 / 15） | 2545（907 / 13） |
| body + 四种位置来源 | 2737（857 / 12） | 3161（1064 / 22） | 3187（1537 / 19） |

路由地板（单条路由 + 一次 `WriteString`）：`http.ServeMux` 130（8 / 1）、chi v5.5.0 301（376 / 3）、fursy 176（24 / 2）。

三列同一天、同一 harness、都关闭访问日志，可以直接比。括号内的 B/op 与 allocs/op 是确定值；ns 列是同一会话 10 次采样的中位数，本轮 95% 置信区间：mie ±12%–27%、fursy ±7%–13%、huma 空 GET ±62%（有一个 2579ns 的离群样本，它的倍率别当结论）。JSON 与 fursy 的差距在这个量级里翻动（多轮 ±10%），别把单轮的 ±3% 当结论。**绝对值和跨库倍率都会漂**：同一台机器上 huma 的代码没动，十一次会话的空 GET 分别是 1315 / 1494 / 1347 / 1544 / 1433 / 1579 / 1218 / 1388 / 1358 / 1339 / 1425ns。第七次会话的「四种来源 huma 4529ns（±36%）」就是离群值，同一台机器安静下来重测是 3190ns（±8%）——单会话的离群值不能当结论。只有同一张表内的三列可比，跨会话只能看数量级。

## 3. 怎么读

- 先减地板（各自耗时 − 各自路由地板）：mie 408 / 2249 / 2607ns，huma 1295 / 1942 / 3031ns，fursy 230 / 2369 / 3011ns。这组数才是「框架层」的对比：空 GET 的框架开销只有 huma 的 32%，但仍是 fursy 的 1.8 倍。
- 端到端（含路由）：空 GET 比 huma 快 2.65 倍、比 fursy 慢 1.33 倍；JSON body 慢 huma 1.15 倍、比 fursy 快 7%（多轮在 ±10% 内翻方向）；四种来源比 huma 与 fursy 都快约 1.16 倍。来源越多差距越小——空请求的差距集中在 ctx 建立与收尾的固定开销，不在绑定本身。
- 分配：3 / 10 / 12 次对 huma 9 / 15 / 22、fursy 4 / 13 / 19。字节：空 GET 33B，低于 huma 的 280B（不复制请求，traceid 也只在真读它时才生成，Content-Type 复用预置切片）；JSON 657B 对 huma 592B；四种来源 857B，低于 huma 的 1064B 与 fursy 的 1537B（请求体按声明长度一次分配，来源解析也复用同一份）。
- 开箱档（`LevelTrace` + `TextHandler`，日志真格式化一遍）只在仓库 `bench_test.go` 里跟踪，不进对比列——huma 与 fursy 都不内置访问日志。默认级别该开在哪一档见 §5。

## 4. 差距拆解（JSON 路径）

- 裸 `json.Unmarshal` 到 8 字段 struct：0.85µs / 0 分配（`encoding/json/v2`），约 JSON 场景 2379ns 的 36%。
- 框架侧每请求分配（空 GET，关访问日志，`pprof -sample_index=alloc_objects`）：`Ctx.Send` 追加 Vary 的切片、`bytes.Clone`（marshal 输出）各 16B，两处共 32B / 2 次；实测总数 33B / 3 次，余下约 1B 是一次零碎分配，可忽略。`Content-Type` 已改成复用预置切片，不再分配；codec 表内的媒体不带参数时也不再走 `mime.ParseMediaType`（它无条件建一个参数 map），改查 codec 表。traceid 的 32B 只在真有人读时才付：`TraceID()` 首次调用生成，开访问日志时即会读。
- 输入侧：请求体按 `Content-Length` 预分配容量（96B 的 body 分配 97B，留 1 字节余量），`Ctx.Body` 把结果缓存在 store 里约 80B，`mime.ParseMediaType` 约 48B（只对带参数或 codec 表外的媒体付，纯 `application/json` 已不付），`http.MaxBytesReader` 约 64B。改前走 `io.ReadAll`，96B 的 body 要 512B、更大 body 还要多付几次扩容与一次收尾拷贝。
- 来源侧：`url.ParseQuery` 约 290B、`readCookies` 约 150B（单个 cookie）。

## 5. 待办优化

按收益/风险排序，每项可独立提交并附前后数据。

1. 默认访问日志级别：现在是 `LevelTrace`，改 `LevelInfo` 后成功请求与路由没命中的 404/405 静默，只留 4xx/5xx 与客户端中止（§3 的 2µs）。

## 6. 复现

```bash
# 本仓库：三段负载 × 两档 Logger（开箱 / 无访问日志），共六个基准
GOMAXPROCS=2 go test -run '^$' -bench . -benchmem -count=10 .

# 跨库对照：三列 + 路由地板
cd ../benchcmp
GOPROXY=https://goproxy.cn,direct GOSUMDB=off GOMAXPROCS=2 go test -run '^$' -bench . -benchmem -count=10
```

## 7. 边界与未测

- 全程 `nopWriter`，没有网络与 TLS；真实请求中这 1µs 量级的差距会被 RTT 摊薄（约占 1ms 总耗时的 0.1%）。
- 未跑 `-cpu=1/8/32`；同机噪声较大：12 线程时同一基准重复跑可差 13–19%，钉到 2 线程后同一场景连跑两轮仍可能差到 10% 以上（第十一次与第十次：空 GET 538 vs 588、四源 2737 vs 2855）；分配数与字节不受这个漂移影响，可跨轮直接比。第七次会话 huma 的四种来源中位数一度带 ±36%（4529ns），安静环境重测降到 3190ns ±8%——离群样本要丢掉重跑，不要写进结论。
- fursy 用发布 tag v0.7.0（radix 自研路由）。它的 `Box` 由路由层自动把 body 交给内部 `binding`（`handler_generic.go` 会调 `ctx.Bind()`），query/header/cookie 要在处理器里手读；mie 与 huma 由 struct tag 驱动。三列断言覆盖了同样的四个来源（`page`、`X-Tag`、`sid`、body），但 fursy 的绑定工作量略低——它不做 struct tag 到查询参数的映射。
- fursy 的 `GET /hello` 不绑定任何来源；它的路由地板 179ns 比 `http.ServeMux` 的 129ns 更高，空 GET 的差距被这两点放大。
