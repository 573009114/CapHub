package core

import (
	"log"
	"net/http"
	"os"

	caphubRouter "caphub/router/caphub"
)

func addr() string {
	v := os.Getenv("ADDR")
	if v == "" {
		return ":8080"
	}
	return v
}

// Run starts http server in a gin-vue-admin-like bootstrap style.
func Run() error {
	h := caphubRouter.Router()
	s := &http.Server{Addr: addr(), Handler: h}
	log.Printf("CapHub Enterprise MVP listening on %s", s.Addr)
	return s.ListenAndServe()
}
