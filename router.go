package main

import (
    "fmt"
    "log/slog"
    "net/http"
    "runtime/debug"
    "slices"
    "time"
)

type Router struct {
    mux      *http.ServeMux
    handlers []Handler

    // ErrorHandler 是全链唯一的错误漏斗：分类、日志、渲染都只在这里发生。
    // 默认实现见 defaultErrorHandler，可用 OnError 整体替换。
    ErrorHandler func(c *Ctx, err error)
}

func New() *Router {
    return &Router{
        mux:          http.NewServeMux(),
        ErrorHandler: defaultErrorHandler,
    }
}

// OnError 替换默认的错误渲染逻辑（例如统一 JSON 错误体、错误码映射、i18n）。
// 它不改变错误的传播路径：所有错误仍然只从 handle 这一个地方进来。
func (r *Router) OnError(h func(c *Ctx, err error)) { r.ErrorHandler = h }

// Use 注册全局中间件。必须在注册路由 / Run 之前调用。
func (r *Router) Use(h Handler) { r.handlers = append(r.handlers, h) }

func (r *Router) Get(pattern string, h Handler)  { r.handle(GET, pattern, h) }
func (r *Router) Post(pattern string, h Handler) { r.handle(POST, pattern, h) }

func (r *Router) handle(method, pattern string, h Handler) {
    r.mux.Handle(method+" "+pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        c := &Ctx{
            Writer:  w,
            Request: req,
            index:   -1,
            chain:   r.chain(h),
        }
        if err := c.Next(); err != nil {
            r.ErrorHandler(c, err)
        }
    }))
}

// chain 固定为：recoverer → 用户中间件 → 路由 handler。
// recoverer 把 panic 转成普通 error，panic 因此和其它错误走同一条路。
func (r *Router) chain(h Handler) []Handler {
    return slices.Concat([]Handler{recoverer}, r.handlers, []Handler{h})
}

// recoverer 是内置的第一道中间件：接住 panic，记日志，返回 error。
// 它自己不做渲染，渲染统一交给 Router.ErrorHandler。
func recoverer(c *Ctx) error {
    var err error

    func() {
        defer func() {
            if p := recover(); p != nil {
                slog.Error(
                    "panic",
                    "method", c.Request.Method,
                    "path", c.Request.URL.Path,
                    "panic", p,
                    "stack", string(debug.Stack()),
                )
                err = fmt.Errorf("panic: %v", p)
            }
        }()

        err = c.Next()
    }()

    return err
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    r.mux.ServeHTTP(w, req)
}

// Run 便利启动。
func (r *Router) Run(addr string) error {
    s := &http.Server{
        Addr:              addr,
        Handler:           r,
        ReadHeaderTimeout: 5 * time.Second,
    }
    return s.ListenAndServe()
}
