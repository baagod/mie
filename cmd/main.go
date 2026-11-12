package main

import (
    "fmt"
    "log"
    "strings"
    "time"

    "github.com/baagod/mie"
)

// logging 是记录请求耗时的中间件示例。
func logging(c *mie.Ctx, _ struct{}) error {
    start := time.Now()
    err := c.Next()
    log.Printf("%s %s %v", c.R.Method, c.R.URL.Path, time.Since(start))
    return err
}

// main 启动带日志中间件的示例服务。
func main() {
    fmt.Println(strings.Split("122", ","))
    // r := mie.New()
    // if err := r.Use(mie.He(logging)); err != nil {
    //     log.Fatal(err)
    // }
    //
    // r.Get("/health", func(_ *mie.Ctx, _ struct{}) (struct{}, error) {
    //     return struct{}{}, nil
    // })
    //
    // r.Get("/hello/{name}", func(_ *mie.Ctx, _ struct{}) (struct{}, error) {
    //     return struct{}{}, nil
    // })
    //
    // if err := r.Run(":8080"); err != nil {
    //     log.Fatal(err)
    // }
}
