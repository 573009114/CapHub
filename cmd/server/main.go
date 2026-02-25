package main

import (
	"log"
	"net/http"
	"os"

	"caphub/internal/caphub"
)

func main() {
	store := caphub.NewStore()
	server := caphub.NewServer(store)
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("CapHub Enterprise MVP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, server.Handler()))
}
