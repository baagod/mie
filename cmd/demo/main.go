package main

import (
	"log"
	"time"

	"mux"
)

func logging(c *mux.Ctx) error {
	start := time.Now()
	err := c.Next()
	log.Printf("%s %s %v", c.Request.Method, c.Request.URL.Path, time.Since(start))
	return err
}

func main() {
	r := mux.New()
	r.Use(logging)

	r.Get("/health", func(c *mux.Ctx) error {
		return c.String(200, "ok")
	})

	r.Get("/hello/{name}", func(c *mux.Ctx) error {
		return c.JSON(200, map[string]string{"hello": c.Param("name")})
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
