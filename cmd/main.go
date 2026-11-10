package main

import (
	"log"
	"time"

	"mux"
)

// logging 是记录请求耗时的中间件示例。
func logging(c *mux.Ctx, in any) (any, error) {
	start := time.Now()
	out, err := c.Next(in)
	log.Printf("%s %s %v", c.R.Method, c.R.URL.Path, time.Since(start))
	return out, err
}

// main 启动带日志中间件的示例服务。
func main() {
	r := mux.New()
	r.Use(logging)

	r.Get("/health", func(_ *mux.Ctx, _ any) (any, error) {
		return nil, nil
	})

	r.Get("/hello/{name}", func(_ *mux.Ctx, _ any) (any, error) {
		return nil, nil
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
