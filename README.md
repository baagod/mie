# mie

[![Go Reference](https://pkg.go.dev/badge/github.com/baagod/mie.svg)](https://pkg.go.dev/github.com/baagod/mie)

一个基于 Go `net/http` 的框架，旨在提供更简洁的 API 设计与开发体验。

> 我猛地扑到电脑前，双手颤抖着抚摸屏幕上的代码块，就像是在抚摸神明的经文。
> 
> 我转过身，噗通一声跪下！仰视的眼神里，全是星星。
> 
> 我不怀疑！我怎么敢怀疑？
> 
> 这字里行间透出的 **实用主义** 哲学与行云流水的 API 设计。这把复杂留给自己，把简单留给开发者的 **温柔霸道** ……
> 
> 这绝对是只有我 —— 咩神大人！才能写得出来。

---

## 安装

```bash
go get github.com/baagod/mie@v0.1.0
```

要求 **Go 1.27.4 或更高**

## 处理器

Go 已经有很多成熟、且性能很好的 HTTP 框架。不过它们大多数处理器只是一个简单回调。如果我们想要自动绑定输入，并通过 `return` 返回响应体时，需要在原始处理器之上包装一层。

这就是 mie 存在的目的之一。在 mie 中，你可以这样写处理器：

```go
type Greeting struct {
    Name string `path:"name"`
}

func main() {
    r := mie.New()
    
    r.Get("/hello/{name}", func(c *mie.Ctx, in Greeting) (string, error) {
        return "hello " + in.Name, nil
    })

    log.Fatal(r.Run(":8080"))
}
```

mie 会根据 `in` 字段 `tag`，按顺序绑定：`body` → `form` ( `url` 优先 ) → `cookie` → `header` → `path`。

然而，在一些场景下，我们并不始终需要绑定输入或必须返回错误。例如，只想在中间件返回错误，并且只在最终处理器返回响应体时，可以这样做：

```go
r.Use(mie.He(func(c *mie.Ctx, _ struct{}) error {
    return mie.Status400BadRequest()
}))

r.Get("/hello", mie.Ho(func(c *mie.Ctx, _ struct{}) string {
    return "hello"
}))
```

通过将输入指定为 `struct{}` 类型，即可忽略绑定。
