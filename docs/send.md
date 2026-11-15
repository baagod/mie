# Send：一个值怎么变成一次响应

只讲 `c.Send(v)` 的内部流程：`ctx.go:163` 的 `Send`、`negotiate.go:13` 的 `negotiate`、`negotiate.go:37` 的 `covers`、`negotiate.go:45` 的 `defaultMedia`。上游适配器、下游错误出口不在本文范围。

## 1. 唯一要记住的规则

一个值只能产出它自己的类型；`Accept` 只能接受或拒绝它，不能要求它变成别的类型。

所以 `Send` 是一次查表：先看值能产出什么（`natural`），再看 `Accept` 里有没有一项覆盖它。

| 值 | natural | 能否原样写字节 |
| --- | --- | --- |
| `string` | `text/plain; charset=utf-8` | 是 |
| `[]byte` | `http.DetectContentType` 嗅探结果 | 是 |
| 实现了 `Renderer` | 它自己决定 | 不走协商 |
| 其它（结构体等） | `application/json` | 否 |

## 2. 决策表

| Accept 项 | string | []byte | 结构体 |
| --- | --- | --- | --- |
| 缺失 或 `*/*` | `text/plain` | 嗅探结果，原样写 | JSON |
| `application/json` | JSON（带引号） | JSON（base64） | JSON |
| `application/*` | JSON（带引号） | 嗅探结果属于 `application/*` 时原样写，否则 base64 | JSON |
| 与 natural 相等（大小写不敏感） | `text/plain` | 原样写 | — |
| `type/*` 与 natural 同族 | `text/*` → `text/plain` | `image/*` + PNG 等 | — |
| 其它具体类型（`text/csv`、`image/webp`） | 跳过 | 跳过 | 跳过 |
| 全部跳完 | 406 | 406 | 406 |

`*/*` 出现在列表中间（`text/html, */*`、`*/*;q=0.8`）也算“没表态”，结果同第一行。

## 3. 四个决定及理由

1. **`Renderer` 排最前。** 它自己设 `Content-Type` 和 body，框架不参与协商，因此也不设 `Vary` —— 响应不随 `Accept` 变。
2. **`raw` 排在整个 `application` 族之前。** PDF 字节的 natural 是 `application/pdf`，客户端发 `application/*` 时应拿到原字节；`covers` 不成立（结构体，或 PNG 收到 `application/*`）才落到下一支。
3. **`application/json` 与 `application/*` 合成同一支兜底。** 两者对剩下的值结果相同（都是 JSON），少一个分支；`raw` 值的 natural 永远不是 `application/json`，所以这支放在 `raw` 之后不会截走点名 JSON 的请求。
4. **`q` 不参与排序。** 我们只产一种格式，`q` 只能表达“多想要”，表达不了“能不能给”，排序不改变结果。`Accept: text/html, application/json` 与反过来结果相同。

## 4. 完整代码

代码与源码逻辑一致，注释是本文为讲解加的。

### 4.1 `Send`（`ctx.go:163-188`）

```go
func (c *Ctx) Send(v any) (err error) {
    if v == nil {
        return // 无 body：收尾时按 c.Status 提交，默认 200
    }

    if r, ok := v.(Renderer); ok {
        return r.Render(c) // 显式渲染覆盖 Accept，不设 Vary
    }

    // 必须在任何 return 之前设：406 也是针对某个 Accept 的 406
    c.W.Header().Add(HeaderVary, HeaderAccept)
    switch media := negotiate(c.GetHeader(HeaderAccept), v); media {
    case "":
        return Status406NotAcceptable() // 空串是唯一失败信号
    case MIMEAppJSON:
        return JSON(v).Render(c) // 先 marshal 再设 Content-Type，失败不污染响应头
    default:
        // 能到这里只可能是 string 或 []byte：negotiate 只在 raw 且被覆盖时返回 natural
        c.Type(media)
        if s, ok := v.(string); ok {
            _, err = c.W.WriteString(s) // 省一次 []byte 转换
            return
        }
        _, err = c.W.Write(v.([]byte))
        return err
    }
}
```

### 4.2 `negotiate`（`negotiate.go:13-34`）

```go
// 按 Accept 书写顺序选第一个能提供的类型；q 不参与比较。
// 返回空串表示没有一项能被这个值满足。
func negotiate(accept string, v any) string {
    natural := defaultMedia(v) // 这个值唯一能产出的类型
    if accept == "" || accept == MIMEAny {
        return natural // 没表态：直接用它，不进循环
    }

    base, _, _ := strings.Cut(natural, ";")           // 去掉 charset 等参数，只留类型
    raw := xtype.Is[string](v) || xtype.Is[[]byte](v) // 能否原样写字节，循环外算一次
    for part := range strings.SplitSeq(accept, ",") { // 逐项切逗号，零分配
        media, _, _ := strings.Cut(part, ";")         // 丢掉 ;q= 与 ;charset=
        switch media = strings.TrimSpace(media); {
        case raw && covers(media, base):
            return natural // 接受值自身类型：原样写字节
        case strings.EqualFold(media, MIMEAppJSON) || strings.EqualFold(media, MIMEAppAny):
            return MIMEAppJSON // application 族兜底：raw 值靠上一支，其余值在这里变 JSON
        case media == MIMEAny:
            return natural // 列表中间的通配，等同没表态
        }
    }

    return "" // 全部跳过：406
}
```

三个分支的顺序就是 §3 的决定；`application` 族这支必须在 `raw` 之后，提到前面 PDF 就会被 base64。

### 4.3 `covers`（`negotiate.go:37-42`）

```go
// 报告 Accept 项是否覆盖 natural：完全相等，或 type/* 覆盖同族。
func covers(media, natural string) bool {
    if strings.EqualFold(media, natural) {
        return true
    }
    // image/* 截成 image/ 再比前缀；natural 已去掉 ;charset，这里只比类型段
    return strings.HasSuffix(media, "/*") && strings.HasPrefix(natural, strings.ToLower(media[:len(media)-1]))
}
```

`media[:len(media)-1]` 把 `image/*` 截成 `image/`，所以 `image/png`、`application/pdf` 都命中；`image/webp` 不命中（它不覆盖 `image/png`，客户端改名不算接受）。

### 4.4 `defaultMedia`（`negotiate.go:45-55`）

```go
// 客户端没表态时，这个值自己产出的类型。
func defaultMedia(v any) string {
    switch v := v.(type) {
    case string:
        return MIMETextPlainUTF8
    case []byte:
        // 头在写 body 前已提交（write.go 的 commit），net/http 不会自动嗅探，只能自己来
        return http.DetectContentType(v)
    default:
        return MIMEAppJSON
    }
}
```

## 5. 端到端

- **结构体 + `application/json`**：200，`application/json`，`{"Name":"bob"}`，带 `Vary: Accept`。
- **string + 无 Accept**：200，`text/plain; charset=utf-8`，body `ok`（无引号）。
- **PNG + `image/*`**：`covers` 认 `image/*` 覆盖 `image/png` → 200，`image/png`，原字节。
- **PDF + `application/*`**：`raw` 且被覆盖 → 200，`application/pdf`，原字节（不会 base64）。
- **`[]byte("hello")` + `application/json`**：JSON 分支命中 → 200，`application/json`，body `"aGVsbG8="`。
- **string + `text/html`**：不覆盖 natural、不是 JSON、不是通配 → 406，body 是 `text/plain` 错误文本。
- **`JSON(user)` + `text/html`**：`Renderer` 先命中 → 200，`application/json`，没有 `Vary`。
- **`nil, nil`**：不写 body，按 `c.Status` 提交，默认 200 空 body。

## 6. 坑

1. **`Vary` 必须在 406 之前设**，否则缓存会把一次 406 当成对所有 `Accept` 都成立。
2. **`Vary` 用 `Add`**，用户自己写的 `Vary` 会保留，响应出现两行；重复在 RFC 下等价于合并，无害。
3. **`[]byte` + JSON = base64**。要原字节，让客户端发覆盖嗅探结果的类型（`image/*`、`application/*`），或不发 `Accept`。
4. **`text/html` 对结构体是 406**。`Send` 不产 HTML，要 HTML 就返回 `Renderer`。
5. **`[]byte` 的 `Content-Type` 只能靠嗅探**，头在写 body 前已提交，net/http 不会补。
6. **`Renderer` 不设 `Vary`**，设了只会让缓存无谓分版本。
7. **畸形头 `text/html,`** 末尾空项被 `TrimSpace` 成空串，不算 `*/*`，跳过。

## 7. 测试地图

| 测试（`renderer_test.go`） | 锁定的行为 |
| --- | --- |
| `TestSendStringWritesText` | string 默认 `text/plain`，body 无引号 |
| `TestSendBytesWritesRaw` | `[]byte` 默认嗅探并原样写 |
| `TestSendNegotiatesStructByAccept` | 缺失、`*/*`、JSON（含大小写）、`application/*`、混合 → 200 JSON；`text/*`、`text/html`、`text/csv` → 406 |
| `TestSendBytesFeedAccept` | `[]byte`/string 与 JSON、`image/png`、`image/*`、`application/pdf`、`application/*`、`text/plain`、`text/*`、`TEXT/PLAIN` 的组合；`image/webp`、`text/csv` → 406 |
| `TestSendRendererIgnoresAccept` | `Renderer` 覆盖 `Accept`，不写 `Vary` |
| `TestSendKeepsExistingVary` | 用户写的 `Vary` 与框架的 `Accept` 并存 |
| `TestSendCommitsPresetStatus` | `c.Status(201)` + `Send` → 201 |
| `TestSendJSONMarshalError` | 编码失败 → 500，响应头未被污染 |

## 8. 自测

1. `Accept: text/plain, application/json`，返回结构体，body 是什么？
2. 返回 `[]byte`，`Accept: */*`，`Content-Type` 由谁决定？
3. 为什么 `Send` 在 `nil` 分支不设 `Vary`？
4. 406 的 body 与 `Content-Type` 是什么，由谁写？
5. `negotiate` 返回空串的条件？
6. PDF 字节 + `application/*` 为什么不是 base64？

<details>
<summary>答案</summary>

1. `text/plain` 对结构体跳过，第二项 JSON 命中 → `{"Name":...}`。
2. `defaultMedia` 调 `http.DetectContentType`；头在写 body 前已提交，net/http 不补。
3. 它不写 body，响应不随 `Accept` 变，设了只会让缓存无谓分版本。
4. `text/plain; charset=utf-8` 的错误文本，带 `nosniff`；由错误出口写，不是 `Send`。
5. 整串 `Accept` 每一项都不能由这个值产生，且没有 `*/*`。
6. `raw` 为真且 `covers("application/*", "application/pdf")` 为真 → 走 `raw` 分支返回 natural；`application/*` 那支只兜底没有 `raw` 的值。

</details>
