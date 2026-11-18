# 基准记录

状态：一次性测量记录，不是性能保证。改动热路径后重跑并更新本文件。

## 1. 测量条件

- 时间：2026-11-18；Windows 11；Intel i7-8700K；Go 1.27.3；GOMAXPROCS=12。
- 命令：`go test -run '^$' -bench . -benchmem -count=3`，取三轮中位数。
- 写入器：`nopWriter`（复用 header map，避免 `httptest.ResponseRecorder` 的分配）；不含网络、TLS 与真实编码器差异。
- 三个场景，payload 相同（8 字段 JSON，约 120 字节）：
  1. `GET /hello`，`IN = struct{}`，只测路由 + 收尾。
  2. `POST /json`，JSON body 解到 8 字段 struct。
  3. `POST /full?page=3&sort=name` + `X-Tag` + `Cookie: sid=abc`，同时覆盖 body、query、header、cookie。
- 对照组：huma v2.40.0（humago 适配 `http.ServeMux`）、fuego v0.21.0、gapi v0.0.0-20260703124147（alpha）。
- 跨库对照代码在仓库外 `../benchcmp`（`replace github.com/baagod/mie => ../mux`），避免把参照库依赖带进本仓库；本仓库根目录的 `bench_test.go` 提供 mie 侧基准。

## 2. 结果

单位：ns/op，括号内为 B/op 与 allocs/op。

| 场景 | mie（默认配置） | mie（关闭访问日志） | huma | fuego | gapi |
|---|---|---|---|---|---|
| 空 GET | 3137（755 / 12） | 1489（673 / 11） | 1340（280 / 9） | 4512（962 / 33） | 1016（569 / 8） |
| JSON body | 6813（2357 / 26） | 5148（2270 / 25） | 2109（593 / 15） | 9644（2481 / 50） | 3210（1217 / 26） |
| body + 四种位置来源 | 8464（3333 / 34） | 6210（3245 / 33） | 3271（1067 / 22） | 13188（2936 / 54） | 4666（2186 / 34） |

“关闭访问日志”是通过 `slog.HandlerOptions{Level: slog.LevelError}` 让访问日志的 `Enabled` 为 false 实现的，代码未改。

## 3. 怎么读

- 默认配置下 mie 比 huma 慢 2.3–3.2 倍，其中 1.6–3.3µs 来自每条请求一行的访问日志（`slog.TextHandler` 输出 7 个属性）；huma、fuego、gapi 默认都不记访问日志。
- 关掉日志后：空 GET 与 huma 只差 11%（1489 vs 1340），JSON 差 2.4 倍，混合来源差 1.9 倍。
- 与 gapi 同档（空 GET 1.5µs vs 1.0µs，JSON 5.1 vs 3.2，混合 6.2 vs 4.7）。
- 全面快于 fuego：约 3.0× / 1.9× / 2.1×，分配数约为其一半（11 vs 33）。
- 分配是主要短板：空 GET 11 次对 huma 9 次、gapi 8 次；JSON 路径 25 次对 huma 15 次。

## 4. 差距拆解（JSON 路径）

- 裸 `json.Unmarshal` 到 8 字段 struct：0.85µs / 0 分配（`encoding/json/v2`）。
- `io.ReadAll` + `Unmarshal`：1.05µs / 2 分配。
- mie 整条 5.15µs → 框架自身约 4.1µs；huma 同路径框架开销约 1.26µs。
- 分配热点的 `pprof -sample_index=alloc_objects` 排序：`newCtx` 6 次（`Ctx`、`store` map、`ResponseWriter`、`context.WithValue`、`Request` 副本、hex traceid）、`mime.ParseMediaType` 2 次、响应头 `Set`/`Add` 各 1 次、`Ctx.Body` 1 次、`Ctx.Set` 1 次。

## 5. 待办优化（均未实施）

按收益/风险排序，每项可独立提交并附前后数据。

1. 访问日志默认关闭或降级：`logAccess` 先判 `Enabled`，默认级别从 Info 调到 Warn，或改为 `Config` 显式开启。收益 −1.6~3.3µs、−1 分配；风险是默认行为变化。
2. `mime.ParseMediaType` 只解析一次：`ServeHTTP` 解析后存进 `Ctx`，`BindBody` 复用。约 −200ns、−1 分配。
3. `store` 懒分配：首次 `Set`/`Bind` 时才 `make`。−1 分配、−48B；只读 handler 全免。
4. 去掉 `context.WithValue`：`Ctx` 已实现 `context.Context`，直接在 `req.WithContext(c)` 上挂载，但需在 `Ctx` 里保存 parent context 兜底 `Value`。−1 分配。
5. traceid 懒生成：有 `X-Request-Id` 时复用，否则到第一次 `TraceID()` 或写日志时再生成。−1 分配。
6. 不建议现在做：`Ctx` + `ResponseWriter` 池化，−2 分配，但要处理 map/chain 重置与生命周期，风险大于收益。

## 6. 复现

```bash
# 本仓库：mie 三个基准
go test -run '^$' -bench . -benchmem -count=5 .

# 跨库对照（网络受限时用镜像与 GOSUMDB=off）
cd ../benchcmp
GOPROXY=https://goproxy.cn,direct GOSUMDB=off go test -run '^$' -bench . -benchmem -count=3
```

## 7. 边界与未测

- 全程 `nopWriter`，没有网络与 TLS；真实请求中 1.5µs 约占 1ms 总耗时的 0.15%，差距会被 RTT 摊薄。
- 未跑 `-cpu=1/8/32`，未用 benchstat 做显著性检验。
- fuego 只能用发布 tag（v0.21.0）；`refs/http/fuego@main` 与它相差 20 个文件，性能量级一致，不做逐行等价声明。
- gapi 为 alpha 版本，其 API 与性能都可能变化。
