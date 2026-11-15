# Handler 设计

状态：契约文档，跟随实现演进。

## 1. 目标

- 类型化：路由声明输入/输出类型，编译期可检查。
- 默认处理器 `func(c *Ctx, in IN) (OUT, error)`；无输入写 `struct{}`；无输出用 `He`。
- 位置来源只认显式 tag；body 交给 Content-Type 对应的解码器（默认 JSON）。
- 外部处理器统一适配成 `Handler`（`func(c *Ctx) error`）后入链。

非目标：自动 OpenAPI/schema、反射路由、`any` 形状的默认处理器、`c.JSON` 手动挂载响应的旧模型。

## 2. 注册规则

- 注册方法返回所在组，可链式追加；`*App` 嵌入根 `Router`，`app.Get` 等价于在根组注册；组不能 `Run`。
- `Group` 的前缀按 `path.Join` 归一，pattern 末尾的 `/` 保留（ServeMux 靠它表示子树）；前缀缺前导 `/` 时补上，`""` 与 `"/"` 都不加前缀；pattern 为空时落在前缀本身。
- 组的中间件是创建时的快照：父组之后再 `Use` 不影响已建组，子组 `Use` 不影响兄弟。
- 中间件声明的 `IN` 在注册时（启动期）与本层之后注册的每条路由的 `IN` 比对（含无输入路由），不一致 panic；`IN` 的字段类型同样在注册时校验。

## 3. 输入绑定（IN）

绑定发生在链上第一个声明 IN 的元素处，一个请求只绑一次并缓存。

- 位置来源只认显式 tag：`path:"id"`、`header:"X-Trace-Id"`、`query:"page"` / `form:"page"`（query 与 form 是同一个参数来源）。没写 tag 的字段不会被位置来源写入。
- 顺序：`path` → `header` → `query`/`form` → body，后写覆盖先写；来源缺失跳过，来源存在但值为空串则清零字段。
- 未导出字段、空 tag 一律跳过；非标量（指针/切片/数组取元素后仍非标量）是声明错误，注册时报 panic。
- 多值来源：切片写全部、数组按序填充（超出丢弃、不足补零）、标量只取第一个。
- 参数来源调用一次 `ParseForm`；它决定是否读 urlencoded body，框架不还原 `c.R.Body`。
- body：非 GET/HEAD 且 `Content-Type` 有已注册解码器时，读 `c.Body()` 解码（默认只注册 `application/json`，应用可注册其它，后注册的覆盖先注册的）；`+json` 后缀（如 `application/vnd.api+json`）回退到 JSON 解码器；CT 缺失/畸形/未注册时不读、不报错。空 body 跳过；无内置上限，超限由 `Ctx.Body()` 映射 413；解码失败 400。
- `struct{}` 不绑不读 body；`*T` 由框架分配，handler 通常拿到非 nil（body 为 `null` 例外）。
- 绑定结果按值缓存：值类型 IN 的每层拿到独立副本，改字段不影响下游（切片/map 的底层数据仍共享，改元素可见）；`*T` 的各层指向同一对象，改对象内容下游可见。
- 参数/头部只扫顶层字段，不展开匿名嵌入；body 的嵌入按解码器规则处理。

## 4. 输出与错误

- error 非 nil 时忽略 OUT；OUT 为 nil 不写 body（中间件 `return nil, nil` 即放行），状态取 `c.Status`，未设置则 200。
- 输出：`Renderer` 自己渲染（`JSON(x)` 显式覆盖 Accept）；其余按 `Accept` 选：`application/json` 时任何值 JSON 编码（`[]byte` 变 base64）；`application/*` 先看值能否原样出字节，能就写原字节，否则 JSON；其它项只在覆盖值自身类型时命中——string 是 `text/plain`，`[]byte` 是嗅探结果，完全相等（大小写不敏感）或 `type/*` 同族才算覆盖，`Content-Type` 用值自身类型，不按客户端改名。`Accept` 缺失或 `*/*` 时直接取值自身类型：string → `text/plain; charset=utf-8`，`[]byte` 原样写，其余 JSON。无法满足时 406，响应带 `Vary: Accept`。
- handler error、bind error 都交给 `Config.ErrorHandler`，最终由 `writeError` 写响应（唯一错误出口）。
- bind 错误：来源绑定与解码失败 400；`http.MaxBytesReader` 超限由 `Ctx.Body()` 映射 413；其它读取错误（连接中断等）500。
- panic 走 `Config.RecoverHandler`。

## 5. 延后

body 重放/流式；严格未知字段；`multipart/form-data`。


