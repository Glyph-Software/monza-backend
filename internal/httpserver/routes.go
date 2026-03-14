package httpserver

import (
	"net/http"

	"monza/backend/internal/handlers"
	"monza/backend/internal/sandbox"
)

func registerAdditionalRoutes(mux *http.ServeMux, mgr *sandbox.Manager) {
	sbxHandler := handlers.NewSandboxesHandler(mgr)
	execHandler := handlers.NewExecuteHandler(mgr)
	filesHandler := handlers.NewFilesHandler(mgr)

	mux.HandleFunc("/api/sandboxes", sbxHandler.HandleCollection)
	mux.HandleFunc("/api/sandboxes/", sbxHandler.HandleItem)
	mux.HandleFunc("/api/sandboxes/{id}/execute", execHandler.HandleExecute)
	mux.HandleFunc("/api/sandboxes/{id}/files/upload", filesHandler.HandleUpload)
	mux.HandleFunc("/api/sandboxes/{id}/files/download", filesHandler.HandleDownload)
}
