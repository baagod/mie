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
- body：非 GET/HEAD 且有 body 时，先按 `Content-Type` 找解码器：`form-urlencoded` 归参数绑定、multipart 归 `Bind`（尚未实现），两者直接跳过；找不到解码器返回 415 且不读 body，`Content-Type` 缺失、畸形同样 415。
- 内置解码器只有 `application/json` 与 `application/xml`，其余媒体由应用填 `Decode`；`+json`/`+xml` 这类结构化后缀（如 `application/vnd.api+json`）回退到同 top-level 的基类型解码器。
- 找到解码器才读 `c.Body()` 解码；无内置上限，超限由 `Ctx.Body()` 映射 413；解码失败 400。
- `struct{}` 不绑不读 body；`*T` 由框架分配，handler 通常拿到非 nil（body 为 `null` 例外）。
- 绑定结果按值缓存：值类型 IN 的每层拿到独立副本，改字段不影响下游（切片/map 的底层数据仍共享，改元素可见）；`*T` 的各层指向同一对象，改对象内容下游可见。
- 参数/头部只扫顶层字段，不展开匿名嵌入；body 的嵌入按解码器规则处理。

## 4. 输出与错误

- error 非 nil 时忽略 OUT；OUT 为 nil 不写 body（中间件 `return nil, nil` 即放行），状态取 `c.Status`，未设置则 200。
- 输出：`Formatted` 只声明格式（`Format() (media, value)`），框架必须用该格式的编码器编码，缺了报错（`JSON(v)`/`XML(v)` 是忽略 `Accept` 的捷径）。`Format()` 返回的媒体必须是 `Config.Codecs` 提供的媒体之一：这里只做精确匹配，vendor 类型（如 `application/vnd.api+json`）不会走 `Accept` 的后缀回退，会直接报没有编码器。
- 裸值：处理器事先设了 `Content-Type` 时以它为准、不协商；否则按 `Accept` 选格式。只有 string 与 `[]byte` 有天然类型（`text/plain`、嗅探结果），天然类型能覆盖请求时优先；其余裸值没有天然类型，按 `Config.Codecs.Order` 选（未配时即 JSON）。`Accept` 里的 `+json`/`+xml` 后缀按同 top-level 的基类型匹配（`application/vnd.api+json` → `application/json`），响应仍写基类型而非 vendor 类型。有编码器就编码，没有编码器时 string/`[]byte` 按原字节写、其余报错。
- 媒体集合：`Config.Codecs` 是固定的媒体表（`JSON`、`XML`、`TOML`、`YAML`、`MsgPack`、`Protobuf`、`CBOR`、`BSON`），填 `Encode` 即能产出、填 `Decode` 即能接收 body，两者独立；字段为零值即不提供该媒体。`New` 给 `JSON`、`XML` 各补内置编码与解码，其余保持零值。`Config.Codecs.Order` 决定通配请求（`type/*`、`*/*`、缺失 `Accept`）的优先级：列出的媒体（`Media`）按给定顺序在前，其余按声明序（JSON、XML、TOML、YAML、MsgPack、Protobuf、CBOR、BSON）追加；未实现的媒体即使列在 `Order` 里也不参与协商，引用未知媒体或重复媒体时 `New` panic。注意 string/`[]byte` 的天然类型仍优先于 `Order`；其余裸值把非 JSON 格式排到前面后，`Accept: */*` 也会落到该格式（例如结构体命中 XML 编码器后可能编码失败）。`New` 把媒体展开成有序列表供 `Formatted` 与 `Accept` 协商共用，`Content-Type` 取协商出的媒体类型。无法满足时 406，响应带 `Vary: Accept`。
- handler error、bind error 都交给 `Config.ErrorHandler`，最终由 `writeError` 写响应（唯一错误出口）。
- bind 错误：来源绑定与解码失败 400；不支持的 `Content-Type` 415；`http.MaxBytesReader` 超限由 `Ctx.Body()` 映射 413；其它读取错误（连接中断等）500。
- panic 走 `Config.RecoverHandler`。

## 5. 延后

body 重放/流式；严格未知字段；`multipart/form-data`。


