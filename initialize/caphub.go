package initialize

import "caphub/internal/caphub"

// CaphubApp creates the application service with all business routes.
func CaphubApp() *caphub.Server {
	store := caphub.NewStore()
	return caphub.NewServer(store)
}
