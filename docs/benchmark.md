# 基准记录

状态：一次性测量记录，不是性能保证。改动热路径后重跑并更新本文件。

## 1. 测量条件

- 时间：2026-11-25；Windows 11；Intel i7-8700K；Go 1.27.4；GOMAXPROCS=12。
- 命令：`go test -run '^$' -bench . -benchmem -count=5`，跑三次会话合并取中位数（每个变体 15 个样本）。
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
| 空 GET | 934（81 / 5） | 1494（280 / 9） | 446（42 / 4） |
| JSON body | 3622（1189 / 14） | 2262（593 / 15） | 2810（907 / 13） |
| body + 四种位置来源 | 4000（1391 / 16） | 3289（1066 / 22） | 3688（1541 / 19） |

路由地板（单条路由 + 一次 `WriteString`）：`http.ServeMux` 139（8 / 1）、chi v5.5.0 394（376 / 3）、fursy 173（24 / 2）。

三列同一天、同一 harness、都关闭访问日志，可以直接比。括号内的 B/op 与 allocs/op 是确定值，最后一次改动分配路径后又单独复测过（同一台机器），比 ns 列新一档；ns 列是三次会话合并的 15 样本中位数。**绝对值跨会话会漂 ±10–20%**（同一台机器上 huma 的代码没动，两次会话之间空 GET 从 1315 漂到 1494），只有同一张表内的比值稳定，跨表比较请只比比值。

## 3. 怎么读

- 先减地板（各自耗时 − 各自路由地板）：mie 795 / 3483 / 3861ns，huma 1355 / 2123 / 3150ns，fursy 273 / 2637 / 3515ns。这组数才是「框架层」的对比：mie 的空 GET 比 huma 省 41%，比 fursy 慢 2.9 倍。
- 端到端（含路由）：空 GET 比 huma 快 37%、比 fursy 慢 2.1 倍；JSON body 慢 huma 1.60 倍、慢 fursy 1.29 倍；四种来源慢 huma 1.22 倍、慢 fursy 1.08 倍。来源越多差距越小——说明差距集中在空请求的固定开销（收尾、响应编码），不在绑定本身。
- 分配：5 / 14 / 16 次对 huma 9 / 15 / 22、fursy 4 / 13 / 19。字节：空 GET 81B 已经低于 huma 的 280B（取消 `context.WithValue` 挂载后不再复制请求）；JSON 与四种来源仍是 1189 / 1391B 对 huma 593 / 1066B，差额集中在读 body 与来源解析。
- 开箱价（`LevelTrace`，日志真格式化一遍）：空 GET 2679、JSON 5894、四种来源 6052ns，比关日志多付 1.7–2.3µs。它不属于对比列，因为 huma 与 fursy 都不内置访问日志；这组数只在 `bench_test.go` 与 §5 的默认级别决策里用。

## 4. 差距拆解（JSON 路径）

- 裸 `json.Unmarshal` 到 8 字段 struct：0.85µs / 0 分配（`encoding/json/v2`），约 JSON 场景 3622ns 的 23%。
- 框架侧每请求分配（空 GET，关访问日志，`pprof -sample_index=alloc_space`）：hex traceid 32B、响应头 `Set` / `Add` 各 16B、`bytes.Clone` 16B（marshal 输出），合计约 81B。
- 输入侧：`io.ReadAll` 约 390B（body 只有 96B）、`mime.ParseMediaType` 约 73B、`http.MaxBytesReader` 约 48B。
- 来源侧：`url.ParseQuery` 约 290B、`readCookies` 约 150B（单个 cookie）。

## 5. 待办优化

按收益/风险排序，每项可独立提交并附前后数据。

1. `mime.ParseMediaType` 只解析一次：`ServeHTTP` 解析后存进 `Ctx`，`BindBody` 复用。约 −200ns、−1 分配。
2. traceid 懒生成：到第一次 `TraceID()` 或写日志时再生成，尤其在没有访问日志时。−1 分配、−32B。
3. 默认访问日志级别：现在是 `LevelTrace`，改 `LevelInfo` 只静默成功请求（§3 的 2µs），4xx/5xx 照记。

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
