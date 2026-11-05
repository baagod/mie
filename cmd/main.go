package main

import (
	"log"
	"time"

	"mux"
)

func logging(c *mux.Ctx, in any) (any, error) {
	start := time.Now()
	out, err := c.Next(in)
	log.Printf("%s %s %v", c.R.Method, c.R.URL.Path, time.Since(start))
	return out, err
}

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
