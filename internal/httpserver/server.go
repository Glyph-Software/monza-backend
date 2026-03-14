package httpserver

import (
	"log"
	"net/http"

	"monza/backend/internal/handlers"
	"monza/backend/internal/sandbox"
)

type Server struct {
	addr    string
	mux     *http.ServeMux
	manager *sandbox.Manager
}

func New(addr string, mgr *sandbox.Manager) *Server {
	mux := http.NewServeMux()
	registerRoutes(mux, mgr)

	return &Server{
		addr:    addr,
		mux:     mux,
		manager: mgr,
	}
}

func (s *Server) Start() error {
	log.Printf("listening on %s", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}

func registerRoutes(mux *http.ServeMux, mgr *sandbox.Manager) {
	mux.HandleFunc("/health", handlers.Health)
	mux.HandleFunc("/hello", handlers.Hello)
	registerAdditionalRoutes(mux, mgr)
}

