package httpserver

import (
	"net/http"

	"monza/backend/internal/handlers"
	"monza/backend/internal/sandbox"
)

func registerAdditionalRoutes(mux *http.ServeMux, mgr *sandbox.Manager) {
	sbxHandler := handlers.NewSandboxesHandler(mgr)

	mux.HandleFunc("/api/sandboxes", sbxHandler.HandleCollection)
	mux.HandleFunc("/api/sandboxes/", sbxHandler.HandleItem)
}

