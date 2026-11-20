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
- 中间件声明的 `IN` 在注册时（启动期）与本层之后注册的每条路由的 `IN` 比对（含无输入路由），不一致 panic；子组是新的注册面，不继承这条约束。
- 路由与中间件的 `IN` 在注册时校验：字段类型非法 panic；`IN` 本身只能是 `T` 或 `*T`，`**T` 是声明错误。
- 只写五个动词就够：`ServeMux` 让 `GET` 隐式匹配 `HEAD`（405 的 `Allow` 头本身就是 `GET, HEAD`），不需要 `Head`；未注册的 `OPTIONS` 返回 405 且带 `Allow`，会落进 `NoMatchHandler`，CORS 预检在那里接管。其余方法（`PROPFIND` 这类）用 `Handle` 注册。

## 3. 输入（IN 绑定与单值读取）

绑定发生在链上第一个声明 IN 的元素处，一个请求只绑一次并缓存。

- 位置来源只认显式 tag：`path:"id"`、`header:"X-Trace-Id"`、`form:"page"`、`cookie:"sid"`。`form` 先读 URL 参数，未命中再读表单参数（含 multipart 的值部分）；同名时 URL 参数优先（整体替换，含空值），未命中才解析 body。字段声明为 `*multipart.FileHeader` / `[]*multipart.FileHeader` 时改读同名文件 part（只认 `form`）。没写 tag 的字段不会被位置来源写入。
- 顺序：body（按 `Content-Type` 解码，权重最低，见下）→ `form` → `cookie` → `header` → `path`，后写覆盖先写，即信任等级从低到高；`cookie` 压过 `form` 是为了让 URL 参数顶不掉 HttpOnly 会话 cookie，`header` 再压过 `cookie` 是因为网关注入的身份头、显式 `Authorization` 比浏览器长期驻留的 cookie 更代表当前请求的意图。来源缺失跳过；空串不特殊处理，能否绑定由目标类型的解析器决定：字符串合法（`?name=` → `""`，`?tags=` → `[""]`），数字、布尔、时间等解析失败返回 400。
- `cookie` 只按字符串读，非法值 400；cookie 头本身格式坏掉时 `net/http` 静默丢弃，不会 400。
- 未导出字段、空 tag 一律跳过；标量，或指针实现 `encoding.TextUnmarshaler` 的类型（`time.Time`、`netip.Addr`、`net.IP` 等）才能绑定，字符串交给该接口解析、解析失败 400；`*multipart.FileHeader` / `[]*multipart.FileHeader` 走文件通道（缺失 nil，单字段收到多个文件 400）；`[]*T` 这类元素层指针、以及取元素后仍不是标量/TextUnmarshaler 的类型，是声明错误，注册时报 panic。
- 字段可加指针（`*int`、`*time.Time`、`*[]int`，多层亦可）：来源缺失保持 nil；值为空串时按类型解析，字符串得到指向空串的指针，数字、时间等解析失败返回 400 且保持 nil（集合类型保留空串元素，如 `?tags=` → `[""]`）。
- 多值来源：切片写全部、数组按序填充（超出丢弃、不足补零）、标量只取第一个；命名切片/数组本身实现 `TextUnmarshaler` 时（如 `net.IP`）按一个值整体解析、多余值丢弃（空值也算第一个值，不会被跳过，如 `?ip=&ip=::1` 得到零值），`[]time.Time`、`[2]time.Time` 这类则逐元素解析。
- 只有 `form` 字段才解析参数：先查 URL（`url.ParseQuery`，非法转义 400），命中不读 body；未命中才解析 body（urlencoded 走 `ParseForm`，会因此读掉 body，框架不还原 `c.R.Body`；multipart 走 `ParseMultipartForm`，缺 boundary、part 损坏都 400）。纯 `header`/`path` 的 IN 既不解 URL 也不读 body。解析结果按请求缓存在 `bind.FormCache`，`Bind` 与 `Ctx.Form` 共用，URL 与 body 各最多解析一次。
- 不建 IN 而只取单个参数时用 `Ctx.Form[T]`：同名 URL 参数优先于表单参数（整体替换，含空值），URL 命中时不读 body，未命中时读取并消费 body（框架不还原 `c.R.Body`）；缺失或解析失败返回零值。`T` 可为标量、`TextUnmarshaler` 或它们的切片/数组/指针，元素层指针按零值返回；`T` 为 `*multipart.FileHeader` / `[]*multipart.FileHeader` 时取上传文件（不读 URL 参数，单类型收到多个文件返回零值）。
- body：非 GET/HEAD 且有 body 时，先按 `Content-Type` 找解码器：`form-urlencoded` 与 `multipart/form-data` 都归参数绑定，两者直接跳过；找不到解码器返回 415 且不读 body，`Content-Type` 缺失、畸形同样 415。
- 内置解码器只有 `application/json` 与 `application/xml`，其余媒体由应用填 `Decode`；`+json`/`+xml` 这类结构化后缀（如 `application/vnd.api+json`）回退到同 top-level 的基类型解码器。
- body 的键名语义由解码器决定：内置 JSON 走 `encoding/json/v2`，无 tag 字段按 Go 字段名参与，键名精确匹配（大小写敏感）；未知键默认忽略，需要严格拒绝时替换 `Config.Codecs.JSON.Decode`。
- 找到解码器才读 `c.Body()` 解码；body 默认上限 1 MiB（`Config.MaxBodyBytes`，负值关闭，multipart 不适用），超限 413；解码失败 400。multipart 的值与不超过 32 MiB 的文件留在内存，超出部分写到磁盘临时文件，请求结束统一删除（handler 自行解析产生的临时文件也一并删除）。
- `struct{}` 不绑不读 body；`*T` 由框架在解码 body 前分配，handler 拿到非 nil（含 `null` body）。
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

## 5. 应用上下文（TypedRouter）

`WithContext[C]` 让处理器的第一个参数变成应用自己的 `C`，绑定、协商、日志、恢复不变。`C` 由工厂从 `*Ctx` 构造，每请求一次，typed 中间件与处理器共享同一份；工厂在首次用到 `C` 的位置执行。完整规格见 [design-typed-router.md](design-typed-router.md)，实现见 `typed.go`。

## 6. 延后

body 重放/流式；严格未知字段。


