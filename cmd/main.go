package main

import (
    "fmt"
    "log"
    "strings"
    "time"

    "mux"
)

// logging 是记录请求耗时的中间件示例。
func logging(c *mux.Ctx, _ struct{}) error {
    start := time.Now()
    err := c.Next()
    log.Printf("%s %s %v", c.R.Method, c.R.URL.Path, time.Since(start))
    return err
}

// main 启动带日志中间件的示例服务。
func main() {
    fmt.Println(strings.Split("122", ","))
    // r := mux.New()
    // if err := r.Use(mux.He(logging)); err != nil {
    //     log.Fatal(err)
    // }
    //
    // r.Get("/health", func(_ *mux.Ctx, _ struct{}) (struct{}, error) {
    //     return struct{}{}, nil
    // })
    //
    // r.Get("/hello/{name}", func(_ *mux.Ctx, _ struct{}) (struct{}, error) {
    //     return struct{}{}, nil
    // })
    //
    // if err := r.Run(":8080"); err != nil {
    //     log.Fatal(err)
    // }
}
