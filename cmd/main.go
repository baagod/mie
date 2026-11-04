package main

import (
	"log"
	"time"

	"mux"
)

func logging(c *mux.Ctx, in any) (any, error) {
	start := time.Now()
	out, err := c.Next(in)
	log.Printf("%s %s %v", c.Request.Method, c.Request.URL.Path, time.Since(start))
	return out, err
}

type health struct {
	Status string `json:"status"`
}

type hello struct {
	Message string `json:"message"`
}

func main() {
	r := mux.New()
	r.Use(logging)

	r.Get("/health", func(c *mux.Ctx, _ struct{}) (health, error) {
		return health{Status: "ok"}, nil
	})

	r.Get("/hello/{name}", func(c *mux.Ctx, _ struct{}) (hello, error) {
		return hello{Message: "hello " + c.Param("name")}, nil
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
