package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/shuijiao1/jiaoprobe/internal/controller/api"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18980", "controller listen address")
	webDir := flag.String("web-dir", "", "optional built web dashboard directory")
	flag.Parse()

	log.Printf("jiaoprobe controller listening on %s", *addr)
	if *webDir != "" {
		log.Printf("serving dashboard from %s", *webDir)
	}
	if err := http.ListenAndServe(*addr, api.NewHandler(api.HandlerOptions{StaticDir: *webDir})); err != nil {
		log.Fatal(err)
	}
}
