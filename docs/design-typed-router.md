# TypedRouter[C] 设计

状态：**已实施**（`typed.go` / `typed_test.go`）。

## 意图

今天所有处理器都收 `*Ctx`。应用要把自己的东西带进处理器（DB 句柄、当前用户、租户），只有两条路：

```go
app := c.Get[*AppCtx](userKey{})   // 运行期，要自己约定 key
// 或者每个处理器开头断言一次
```

`WithContext[C]` 让应用在**注册期**声明一次「怎么从 `*Ctx` 造出 `C`」，之后处理器直接收 `C`，类型在编译期确定。

```go
app := r.WithContext(func(c *mux.Ctx) *AppCtx {
    return &AppCtx{Ctx: c, DB: globalDB, User: c.Get[*User](userKey)}
})

app.Get("/me", func(c *AppCtx, _ struct{}) (User, error) {
    return c.User, nil
})
```

## 非目标

- 不做 per-route 元数据、OpenAPI、路由字符串解析。
- 不让 `C` 替换 `*Ctx`：`C` 必须从 `*Ctx` 构造，绑定/协商/日志/恢复这些能力不变。
- 不做包级全局注册表。`d6879a4` 已经把 `bind.Register` 和 `Encoders map[string]Encoder` 换成固定槽位，理由是手写注册要承担归一化、去重、排序、与内置冲突判定六类维护；这里不倒退。
- 不做第二套 IN 校验，也不让 API 变成 16 个方法（`Get`/`GetBind` 各一套）：前者让两条注册面的规则迟早不一致，后者与 `Router` 分裂、同一个处理器写法不能通用。

## 方案

选中 **A**：与 `Router` 同形，只把第一个参数从 `*Ctx` 换成 `C`，无输入路由写 `func(c *AppCtx, struct{})`，`IN` 靠类型推断，不需要第二个方法名。

落选 **B**（收窄 `OUT`，`Get`/`GetBind` 各一套）：方法数翻倍、`OUT` 退化成 `any`、`He` 适配器要再造一套；而 A 让 `He`/`handler` 原样复用，同一个处理器写法在两种 Router 上通用。

## 已确认的决定

- **D1 工厂执行时机：懒执行。** `factory` 懒在 `typedHandler` 的闭包里：该路由第一次处理请求时才执行，没人用 `C` 的路由不执行工厂；工厂内 panic 落在链内，`c.index` 能反映停在哪个中间件。
- **D2 typed 中间件签名：`Use[I any](m TypedHandler[C, I, any])`。** 中间件靠 `C` 上的 `Next()` 推进链（通常由嵌入 `*mux.Ctx` 提升而来）；这条要求由类型参数 `TypedContext` 保证，缺 `Next() error` 在编译期就报错。
- **D3 `WithContext` 返回快照。** 复制 `prefix`/`middlewares`/`in`（与 `Group` 走同一个 `Router.snapshot`，`router.go:102-110`），typed 面 `Use` 不外溢到原 `Router`，两个 `WithContext` 互不影响。
- **D4 工厂签名保持 `func(*Ctx) C`，不返回 error。** 拒绝请求只发生在中间件（不调 `Next` 即可）；工厂只做组装，`typedHandler` 里没有新的错误分支。

## 已知代价

- 两套注册面：`Router` 与 `TypedRouter[C]`，处理器签名不同（`func(*Ctx, IN)` 与 `func(C, IN)`）。typed 面覆盖五个动词与 `Handle`，自定义方法不必退回 `Router`。
- 评估过更彻底的形态：让 `Router` 自己带 `C`（`type Router[C any] struct`，`App` 内嵌 `Router[*Ctx]`，`WithContext[D]` 换 `C`），已实测可编译。放弃的理由是类型参数会扩散到导出的 `HandlerFunc`——要么泛型化成 `HandlerFunc[C, IN, OUT]`，要么保留两套处理器类型；爆炸半径比新增 `typed.go` 大。而重复面的规模由 HTTP 方法集封闭（mie 只有五个动词加一个 `Handle`），不会随迭代增长。

## 设计

### 实现要点

`TypedRouter` 的五个动词与 `Handle` 各一行委托给 `Router`，`Use` 与它们共用 `typedHandler`（把 `TypedHandler[C, IN, OUT]` 包成 `HandlerFunc[IN, OUT]`），`Group` 只换底层 `Router`、工厂跟随。签名与函数体以 `typed.go` 为准，不在此处复制。

### 机制：类型键缓存（与 Bind 同构）

`typedHandler` 用 `typedCtxKey[C]{}`（零尺寸结构体，类型即键）把工厂结果写进 `c.store`，与 `bindKey[IN]{}` 同构，值/指针语义也一致。三条性质全部来自这一处：

- **工厂每请求最多跑一次**：链上任意多个中间件与处理器共享同一个值。
- **值/指针语义与 IN 一致**：`C = AppCtx` 时存副本，中间件改自己的参数不会传下去；`C = *AppCtx` 时整链共享同一对象，任何一处的写入对下游可见。不需要第二套解释。
- **不经过 `c.Value`**：`Value` 会回退到 `req.Context()`，把框架内部键暴露进 context 查找路径，没必要。

### 校验

- `factory == nil`：注册期 panic，与 `bind.Validate` 的失败方式一致。
- IN 与中间件 IN 的一致性：完全沿用 `Router.Use` 的 `r.in` 校验（`router.go:26-31`、`router.go:72-79`），TypedRouter 不新增规则。
- `C` 的可推进性：`TypedRouter[C TypedContext]` 把「方法集必须包含 `Next() error`」写进类型参数，构造时就由编译器检查。嵌入 `*mux.Ctx` 时 `AppCtx`、`*AppCtx` 与自行声明该方法的接口类型都满足；值类型 `C` 也能注册，只是中间件改的是副本，写入不会传下去。

### 成本

- 用了 `WithContext` 的路由：每请求多一次 map 查找 + 一次类型断言，量级与 `Bind` 相同。
- 没用 `WithContext` 的应用：零改动、零成本（`store` 仍是懒创建）。

### 边界

- 同一个 `Router` 调两次 `WithContext`、不同 `C`：两个独立键，互不影响。
- 工厂看到的是**首次被调用那一刻**的 `store`。D1 懒执行下工厂在链上首次使用 `C` 的位置运行，所以它依赖的中间件（如写入 `*User` 的鉴权）必须排在 typed 中间件之前，否则工厂读到空值。
- `C` 是接口类型时（接口自己声明了 `Next() error`），该接口的所有 `WithContext` 共用 `typedCtxKey[I]{}` 一格；别用方法集过宽的接口做 `C`。
- `Ctx` 不跨请求，`store` 随请求结束，不存在跨请求串值。

## 组合

`C` 之间靠 Go 的结构体嵌入组合，框架不做特殊处理：`type AppCtx struct { *authctx.AuthCtx; DB *sql.DB }` 自动获得内层的方法与 `Next()`，工厂写 `&AppCtx{AuthCtx: authctx.New(base), DB: db}` 即可；`*AuthCtx` 与 `*AppCtx` 是两个不同的 `C`，各自的 typed 中间件不能互换。

第三方包应当成对提供「中间件 + 工厂」：中间件把 `User` 这类请求态写进 `store` 并用错误拒绝，工厂只做组装。工厂没有错误通道，且它在链上早于 typed 中间件执行——让工厂承担拒绝，会跳过本该运行的用户中间件。

## 已定的实现选择

- 不加 `HeC[C, IN]`：typed 处理器自己写 `(any, error)` 即可，真用到再议。
- `typed.go` 单独成文件；`Router` 只多一个 `snapshot`（`router.go:102-110`），`Group` 与 `WithContext` 共用它。

## 测试

行为用例在 `typed_test.go`（17 个），这里只记不显然的三条：`C` 为值类型时 `Use` 能注册，但中间件写入随副本丢弃；typed 中间件与处理器共享同一份绑定缓存；工厂返回 nil 时 panic，不静默回零值（检查只覆盖接口类型的 nil，指针类型的 nil 会落到下游解引用）。
