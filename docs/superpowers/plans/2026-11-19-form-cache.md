# 表单解析缓存：query/form 两张表 + Bind 复用

## Goal

同一请求内 query 与 body 各解析一次；`Ctx.Form` 与 `Ctx.Bind` 共用 `Ctx.form`（`bind.FormCache`，
内含 query 与 body 两张表）；`form` 的取值语义保持“query 覆盖 body”。对外只保留 `form` tag，
`query` 来源删除。

## 当前事实

- `ctx.go:144-158`：每次 `Ctx.Form` 都调 `c.R.URL.Query()`；未命中再 `bind.ParseForm(c.R)`。
- `internal/bind/decoder.go:36-56`：`plan.bind` 每次 `url.ParseQuery(r.URL.RawQuery)`。
- `internal/bind/decoder.go:106-144`：`bindForm` 每次 `ParseForm`，再手工用 query 覆盖 `PostForm`。
- `internal/bind/bind.go:36-41` `ParseForm`、`:45-62` `ParseFile`（内部又调一次 `ParseForm`）。
- 标准库只保证 body 读一次（`ParseMultipartForm` 后二次调用直接返回），入口仍在反复调用；
  `Ctx.Form` 吞掉解析错误后，`Bind` 对已消费的 body 再解析会拿到空值并返回 200。
- 基线（2026-11-19）：`go build ./... && go vet ./... && go test ./... -count=1` 全绿。

## 决策

- **D6 两张表，一个载体**：`bind.FormCache` 持有 `query`（URL 参数）与 `form`（`r.PostForm`，纯 body）。
  `form` tag 读取时用 query 覆盖（沿用 `decoder.go:133-136`），不预先合并、不复制 `PostForm`。
- **D7 懒解析，bind 主动拉**：`Decode` 遍历到 form 源时由 `bindForm` 逐字段取表：query 命中直接绑，
  未命中才 `c.Form(r)`；只绑 path 的 multipart 不读 body。
- **D8 文件不进表**：`*multipart.FileHeader` 仍从 `r.MultipartForm` 取。
- **D9 单错误缓存**：解析错误只进 `FormCache.err`（先到先得），`Query` / `Form` 不返回错误；
  一旦有错，两张表都返回空表、不再解析，`bindForm` 取值时当场返回。`Form` 吞错返回零值，`Bind` 用这条错误返回 400/413，
  避免“先 Form 失败、后 Bind 对已消费 body 二次解析得到 200”。
- **D10 一个缓存类型两处用**：`Ctx` 持有 `bind.FormCache`；`bind.Decode(r,in,cache)` 的 cache 为 nil 时内部用局部 `FormCache`。
  不存在 `Needs` / `DecodeMaps` 那种“先加载再解码”的时序契约。
- **D11 只保留 form tag**：删除 `query` 来源与 `bindQuery`；`form` 字段先查 URL，命中即绑，未命中才解析 body。
  GET 由 `ParseForm` 自动只取 query；POST 同时得到两表。代价：纯 URL 字段在缺值时也会读 body，
  且 body 可补 query 缺值。

## 四问四答

1. 消除什么：每次 Form/Bind 重复的 `url.ParseQuery`、`mime.ParseMediaType`、`ParseForm` 入口；
   同一请求 form/query 各解析多次。
2. 不引入坏在哪：功能不会坏（body 本来只读一次），但解析状态分散在 Form/Bind 两处，
   错误无法复用，且每轮都多付一次 Content-Type 解析与调用开销。
3. 删除什么：`Decode` 里每轮的 `url.ParseQuery` / `ParseForm`、`bindForm` 的 `ParseForm`、
   `ParseFile` 的 `ParseForm`、`Ctx.Form` 的 `URL.Query` 调用；以及初稿里的 `Needs` /
   `DecodeMaps` / `FormValues` 三个导出符号。
4. 现在有吗：有——用户明确要求“一次解析、两张表复用”。

## 数据流

```
Ctx.Form ── c.form.Query(c.R) ── url.ParseQuery，一次
        └─ c.form.Form(c.R)  ── ParseForm，一次；返回 r.PostForm
                                  │
                                  └─ r.MultipartForm（文件来源，不复制进表）

Ctx.Bind ── bind.Decode(c.R, in, &c.form)
                 │
                 └─ Decode 遍历到 form 源时由 bindForm 按字段向 cache 要表
```

## API 变更

`internal/bind`

```go
type FormCache struct {
    query, form url.Values
    err error
}

// Query 解析 URL 参数并缓存；错误存入 err，不打断取值。
func (c *FormCache) Query(r *http.Request) url.Values

// Form 解析 body 并缓存 r.PostForm；query 由调用方按需通过 Query 取。
func (c *FormCache) Form(r *http.Request) url.Values

// 缓存可选：nil 用一次性缓存，传入则复用。
func Decode[IN any](r *http.Request, in *IN, cache *FormCache) error

// 不再解析，只从已解析的文件表取字段。
func ParseFile[T any](m *multipart.Form, name string) (v T, err error)
```

`ctx.go`

```go
type Ctx struct {
    // 现有字段不动
    form bind.FormCache
}
```

`Decode(r, in, nil)` 用一次性缓存；测试 20 处两参调用补 `nil`，`cache_test.go` 传 `&cache` 复用。

## Task 1：新增 bind.FormCache

文件：`internal/bind/cache.go`（新建）

1. 结构与两个方法：

```go
type FormCache struct {
    query, form url.Values
    err error
}

func (c *FormCache) Query(r *http.Request) url.Values {
    if c.query == nil {
        var e error
        c.query, e = url.ParseQuery(r.URL.RawQuery)
        if c.err == nil {
            c.err = e
        }
    }
    return c.query
}

func (c *FormCache) Form(r *http.Request) url.Values {
    if c.form == nil {
        if e := ParseForm(r); c.err == nil {
            c.err = e
        }
        if r.PostForm == nil {
            r.PostForm = url.Values{}
        }
        c.form = r.PostForm
    }
    return c.form
}
```

2. 零值可用，不新增构造器；解析错误只进 `err`，先到先得，不打断取值。

测试（`internal/bind/cache_test.go`）：

- `TestFormCacheQuery`：两次 `Query` 结果一致；坏 RawQuery 仍保留已解析的 key。
- `TestFormCacheForm`：urlencoded body 后 `r.PostForm` 可读；multipart 请求下 `r.MultipartForm` 就绪。
- `TestFormCacheCachesError`：body `a=%zz` 后 `cache.err` 非 nil，第二次 `Form` 不重读 body。

## Task 2：bind 改为向 cache 取表

文件：`internal/bind/decoder.go`、`internal/bind/bind.go`

1. 删除 `plan.bind`，遍历直接放进 `Decode`（它只有这一个调用点），`cache` 为 nil 时用一次性缓存：

```go
func Decode[IN any](r *http.Request, in *IN, cache *FormCache) (err error) {
    if in == nil {
        return errors.New("bind: destination must be a non-nil pointer to IN")
    }

    p, e := planFor(reflect.TypeFor[IN]())
    if e != nil {
        return e
    }

    v := reflect.ValueOf(in).Elem()  // IN
    if v.Kind() == reflect.Pointer { // 请求期分配 T，handler 永远拿到非 nil 指针。
        if v.IsNil() {
            v.Set(reflect.New(v.Type().Elem()))
        }
        v = v.Elem()
    }

    if cache == nil {
        cache = &FormCache{}
    }

    for _, s := range p.sources {
        if len(s.fields) == 0 {
            continue
        }
        if err = s.bind(s, r, v, cache); err != nil {
            return err
        }
    }

    return
}
```

解析错误由 `return c.err` 统一抛出；来源绑定错误优先。

2. `bindForm` 去掉 `ParseForm`，按字段懒取：

```go
ss, ok := c.Query(r)[f.key]
if !ok {
    c.Form(r)
    ss = r.PostForm[f.key]
}
```

3. `ParseFile` 接收 `*multipart.Form`（nil 视为无文件），删除内部 `ParseForm`。
4. `ParseForm` 注释改为“由 `FormCache` 调用”。

测试：

- 既有 `internal/bind` 约 20 处 `Decode` 调用不改，必须全绿。
- `TestParseFile` 改为先用 `FormCache.Form` 解析，再传 `r.MultipartForm`。
- 新增 `TestDecodeSharesCache`：`Decode(r, in, &cache)` 后 `cache.Form` 不重复读 body（计数 reader）。

## Task 3：Ctx 接线

文件：`ctx.go`

1. `Ctx` 结构体（`ctx.go:37-47`）加一个字段 `form bind.FormCache`。
2. `Form` 改为：

```go
func (c *Ctx) Form[T any](name string) (v T) {
    if bind.IsFile[T]() {
        c.form.Form(c.R)
        v, _ = bind.ParseFile[T](c.R.MultipartForm, name)
        return
    }
    q := c.form.Query(c.R)
    if ss, ok := q[name]; ok {
        v, _ = bind.Parse[T](ss)
        return
    }
    f := c.form.Form(c.R)
    v, _ = bind.Parse[T](f[name])
    return
}
```

3. `Bind` 只改一处：

```go
if err = bind.Decode(c.R, in, &c.form); err != nil {
    return bindError(err)
}
```

`bindError` 抽出现有的 413/400 映射。

测试（`ctx_test.go`）：

- `TestCtxFormBadBodyThenBind`：body `a=%zz`，先 `Form` 得零值，再 `Bind` 必须 400；当前实现会 200 空值，先红。
- `TestCtxFormThenBindSharesValues`：body `name=form`，先 `Form` 后 `Bind`，两次都是 `form`。
- `TestBindPathOnlySkipsMultipartBody`：multipart 请求、IN 只有 path 字段，body reader 计数必须为 0。
- `TestCtxFormMultipartValue` / `TestCtxFormMultipartFile` 保持绿。

## Task 4：文档

- `docs/design-handler.md`：`Ctx.Form` 条目写“query 未命中会读 body”；`Bind` 条目写“解析结果缓存在 `bind.FormCache`，同一请求最多各解析一次”。
- `docs/superpowers/specs/2026-11-18-multipart-form-design.md` 加 D6：`bind.FormCache` 持有两张表、懒解析、单错误缓存；`form` tag 仍在读取时叠加 query。

## Task 5：删除 query tag，只留 form

文件：`internal/bind/plan.go`、`internal/bind/decoder.go`、测试、文档

1. `plan.go`：删 `bindQuery` 常量与 source 项；来源顺序改为 form < cookie < header < path，信任注释同步。
2. `decoder.go`：删 `bindQuery` 方法；`source.bind` 的最后一个参数由 `query url.Values` 改成 `cache *FormCache`；
   `bindForm` 逐字段先查 `cache.Query(r)[f.key]`，命中直接 `setValues`，未命中才 `cache.Form(r)` 并在
   `r.PostForm` 取值（文件 part 校验放在这条未命中路径上）；文件字段仍先 `cache.Form(r)` 再取 `r.MultipartForm`。
3. `Decode` 删除 `query` 变量与 tag 判断，循环只调 `s.bind(s, r, v, c)`，末尾仍 `return c.err`。
4. 测试：`query:"x"` 统一改 `form:"x"`；同一字段同时写 `form:"x" query:"x"` 的合并成一个 `form:"x"`；
   只测 query 来源错误文案的用例改成 form 文案或删除。
5. 文档：`docs/design-handler.md` 的 tag 列表删 `query`，`form` 条目写明“URL 参数优先、缺失回退 body”；
   spec 的 D6/D7 同步。

## 验证

```bash
go build ./...
go vet ./...
go test ./... -count=1
go test . -run 'TestCtxForm|TestBind' -count=1
```

手工场景（可写成临时测试后删除）：

- `GET ?a=1&b=2` + body `b=3` → `Form("a")=1`、`Form("b")=2`、`form:"b"` 绑定得 2。
- 同一请求先 `Form` 后 `Bind`，body 只解析一次，值一致。
- 坏 body：`Form` 零值，随后 `Bind` 400。
- 仅 path 的 multipart：body 零读取。

## 风险

- `form` tag 仍被 query 覆盖（`bind_test.go:530/547/743` 锁定）；若改严格隔离，这 3 条要一起改，本计划不选。
- `Form` 遇坏 body 后 `Bind` 变为 400（原为 200 空值），这是修正，需 `TestCtxFormBadBodyThenBind` 锁定。
- query 解析错误时 `Form` 返回空表（已有错误不再解析），`Bind` 400；错误由 `bindForm` 顺手返回，来源绑定错误优先。
- 工作树有并发未提交改动（app.go、ctx.go、router.go、write.go、write_test.go），只改本计划列出的片段。

## 规模

`internal/bind/cache.go` +45；`internal/bind/decoder.go` +14/−15；`internal/bind/bind.go` +4/−8；`ctx.go` +11/−7（含并发改动）；测试 +150；文档 +12。
