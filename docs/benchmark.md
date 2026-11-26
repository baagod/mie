# 基准记录

状态：一次性测量记录，不是性能保证。改动热路径后重跑并更新本文件。

## 1. 测量条件

- 时间：2026-11-26（第三次复测）；Windows 11；Intel i7-8700K；Go 1.27.4；GOMAXPROCS=12。
- 命令：`go test -run '^$' -bench . -benchmem -count=5`，取中位数（每个变体 5 个样本）。
- 写入器：`nopWriter`（复用 header map，避免 `httptest.ResponseRecorder` 的分配）；不含网络、TLS 与真实编码器差异。
- 本表三列全部由仓库外 `../benchcmp` 在同一进程、同一 harness 里跑出（`replace github.com/baagod/mie => ../mux`），跨库倍率只在这张表内成立。仓库根目录的 `bench_test.go` 跑同样的 mie 两档，请求构造与 writer 不同，绝对数不能跨表比，只用于本仓库的回归跟踪。
- 三列都关闭访问日志：`mie` 用 `slog.New(slog.DiscardHandler)`（`Enabled` 恒 false，框架零日志开销），huma 与 fursy 本来就不内置访问日志。mie 开箱配置（`TextHandler(io.Discard)` 开在 `LevelTrace`，日志真格式化一遍再丢字节、不付 `write(2)`）只在 `bench_test.go` 里跟踪，不进对比表——见 §3 末尾。
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
| 空 GET | 846（49 / 4） | 1347（280 / 9） | 465（42 / 4） |
| JSON body | 3282（1157 / 13） | 2085（593 / 15） | 2595（907 / 13） |
| body + 四种位置来源 | 3438（1359 / 15） | 3076（1066 / 22） | 3395（1542 / 19） |

路由地板（单条路由 + 一次 `WriteString`）：`http.ServeMux` 128（8 / 1）、chi v5.5.0 405（376 / 3）、fursy 176（24 / 2）。

三列同一天、同一 harness、都关闭访问日志，可以直接比。括号内的 B/op 与 allocs/op 是确定值；ns 列是同日单会话 5 次采样的中位数。**绝对值跨会话会漂 ±10–20%**（同一台机器上 huma 的代码没动，三次会话的空 GET 分别是 1315 / 1494 / 1347），只有同一张表内的比值稳定，跨表比较请只比比值。

## 3. 怎么读

- 先减地板（各自耗时 − 各自路由地板）：mie 718 / 3154 / 3310ns，huma 1219 / 1957 / 2948ns，fursy 289 / 2419 / 3219ns。这组数才是「框架层」的对比：mie 的空 GET 比 huma 省 41%，比 fursy 慢 2.5 倍。
- 端到端（含路由）：空 GET 比 huma 快 37%、比 fursy 慢 1.82 倍；JSON body 慢 huma 1.57 倍、慢 fursy 1.26 倍；四种来源慢 huma 1.12 倍、慢 fursy 1.01 倍。来源越多差距越小——说明差距集中在空请求的固定开销（收尾、响应编码），不在绑定本身。
- 分配：4 / 13 / 15 次对 huma 9 / 15 / 22、fursy 4 / 13 / 19。字节：空 GET 49B，低于 huma 的 280B（不复制请求，traceid 也只在真读它时才生成）；JSON 与四种来源是 1157 / 1359B，对 huma 593 / 1066B、fursy 907 / 1542B，差额集中在读 body 与来源解析。
- 开箱价（`LevelTrace`，日志真格式化一遍）：空 GET 2591、JSON 5131、四种来源 5513ns，比关日志多付 1.7–2.1µs。它不属于对比列，因为 huma 与 fursy 都不内置访问日志；这组数只在 `bench_test.go` 与 §5 的默认级别决策里用。

## 4. 差距拆解（JSON 路径）

- 裸 `json.Unmarshal` 到 8 字段 struct：0.85µs / 0 分配（`encoding/json/v2`），约 JSON 场景 3282ns 的 26%。
- 框架侧每请求分配（空 GET，关访问日志，`pprof -sample_index=alloc_space`）：响应头 `Set` / `Add` 各 16B、`bytes.Clone` 16B（marshal 输出），三处共 48B / 3 次；实测总数 49B / 4 次，余下约 1B 是一次零碎分配，可忽略。traceid 的 32B 只在真有人读时才付：`TraceID()` 首次调用生成，开访问日志时即会读。
- 输入侧：`io.ReadAll` 约 390B（body 只有 96B）、`mime.ParseMediaType` 约 73B、`http.MaxBytesReader` 约 48B。
- 来源侧：`url.ParseQuery` 约 290B、`readCookies` 约 150B（单个 cookie）。

## 5. 待办优化

按收益/风险排序，每项可独立提交并附前后数据。

1. `mime.ParseMediaType` 只解析一次：`ServeHTTP` 解析后存进 `Ctx`，`BindBody` 复用。约 −200ns、−1 分配。
2. 默认访问日志级别：现在是 `LevelTrace`，改 `LevelInfo` 后成功请求与路由没命中的 404/405 静默，只留 4xx/5xx 与客户端中止（§3 的 2µs）。

## 6. 复现

```bash
# 本仓库：三段负载 × 两档 Logger（开箱 / 无访问日志），共六个基准
go test -run '^$' -bench . -benchmem -count=5 .

# 跨库对照：三列 + 路由地板
cd ../benchcmp
GOPROXY=https://goproxy.cn,direct GOSUMDB=off go test -run '^$' -bench . -benchmem -count=5
```

## 7. 边界与未测

- 全程 `nopWriter`，没有网络与 TLS；真实请求中这 1µs 量级的差距会被 RTT 摊薄（约占 1ms 总耗时的 0.1%）。
- 未跑 `-cpu=1/8/32`，未用 benchstat 做显著性检验；同机噪声较大（同一基准重复跑可差 5–10%）。
- fursy 用发布 tag v0.7.0（radix 自研路由）。它的 `Box` 只把 body 交给内部 `binding`，query/header/cookie 要在处理器里手读；mie 与 huma 由 struct tag 驱动。三列断言的字段相同（`page`、`sid`），但绑定工作量略有差异。
- fursy 的 `GET /hello` 不绑定任何来源，其路由地板也是三者里最低的 179ns，空 GET 的差距被这两点放大。
