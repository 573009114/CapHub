package caphub

import (
	"net/http"

	"caphub/initialize"
)

// Router returns the caphub http handler.
func Router() http.Handler {
	return initialize.CaphubApp().Handler()
}
