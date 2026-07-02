package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/shuijiao1/jiaoprobe/internal/controller/api"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18980", "controller listen address")
	flag.Parse()

	log.Printf("jiaoprobe controller listening on %s", *addr)
	if err := http.ListenAndServe(*addr, api.NewHandler()); err != nil {
		log.Fatal(err)
	}
}
