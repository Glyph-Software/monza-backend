package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"monza/backend/internal/devcontainer"
	"monza/backend/internal/sandbox"
)

// devcontainerBasePath returns the directory containing devcontainer templates.
// In Docker it is /app/devcontainers; locally it is devcontainers (relative to cwd).
func devcontainerBasePath() string {
	if p := os.Getenv("DEVCONTAINERS_PATH"); p != "" {
		return p
	}
	return "devcontainers"
}

type SandboxesHandler struct {
	manager      *sandbox.Manager
	configCache  sync.Map // template name -> *devcontainer.Config
}

func NewSandboxesHandler(m *sandbox.Manager) *SandboxesHandler {
	return &SandboxesHandler{manager: m}
}

// getDevcontainerConfig returns a parsed devcontainer config for the template,
// using an in-memory cache to avoid repeated disk reads.
func (h *SandboxesHandler) getDevcontainerConfig(template string) (*devcontainer.Config, error) {
	if cached, ok := h.configCache.Load(template); ok {
		return cached.(*devcontainer.Config), nil
	}
	configPath := filepath.Join(devcontainerBasePath(), template, "devcontainer.json")
	cfg, err := devcontainer.ParseFile(configPath)
	if err != nil {
		return nil, err
	}
	h.configCache.Store(template, cfg)
	return cfg, nil
}

type createSandboxRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

func (h *SandboxesHandler) HandleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		log.Printf("HTTP %s %s - list sandboxes", r.Method, r.URL.Path)
		h.list(w, r)
	case http.MethodPost:
		log.Printf("HTTP %s %s - create sandbox", r.Method, r.URL.Path)
		h.create(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *SandboxesHandler) HandleItem(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/sandboxes/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		log.Printf("HTTP %s %s - missing sandbox id", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}

	idStr := parts[0]
	id, err := uuid.Parse(idStr)
	if err != nil {
		log.Printf("HTTP %s %s - invalid sandbox id %q: %v", r.Method, r.URL.Path, idStr, err)
		http.Error(w, "invalid sandbox id", http.StatusBadRequest)
		return
	}

	// /api/sandboxes/:id
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			log.Printf("HTTP %s %s - get sandbox %s", r.Method, r.URL.Path, id)
			h.get(w, r, id)
		case http.MethodDelete:
			log.Printf("HTTP %s %s - delete sandbox %s", r.Method, r.URL.Path, id)
			h.delete(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/sandboxes/:id/heartbeat
	if len(parts) == 2 && parts[1] == "heartbeat" && r.Method == http.MethodPost {
		log.Printf("HTTP %s %s - heartbeat for sandbox %s", r.Method, r.URL.Path, id)
		h.heartbeat(w, r, id)
		return
	}

	http.NotFound(w, r)
}

func (h *SandboxesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("create sandbox - invalid request body: %v", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Template == "" {
		req.Template = "go"
	}

	if req.Name == "" {
		req.Name = "sandbox-" + req.Template
	}

	cfg, err := h.getDevcontainerConfig(req.Template)
	if err != nil {
		log.Printf("create sandbox - failed to load devcontainer template %q: %v", req.Template, err)
		http.Error(w, "failed to load devcontainer template: "+err.Error(), http.StatusBadRequest)
		return
	}

	sb, err := h.manager.CreateFromDevcontainer(r.Context(), req.Name, "", cfg)
	if err != nil {
		log.Printf("create sandbox - manager error: %v", err)
		http.Error(w, "failed to create sandbox", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusAccepted, sb)
}

func (h *SandboxesHandler) list(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := h.manager.ListSandboxes(r.Context())
	if err != nil {
		log.Printf("list sandboxes - error: %v", err)
		http.Error(w, "failed to list sandboxes", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, sandboxes)
}

func (h *SandboxesHandler) get(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	sb, err := h.manager.GetSandbox(r.Context(), id)
	if err != nil {
		log.Printf("get sandbox %s - error: %v", id, err)
		http.Error(w, "failed to get sandbox", http.StatusInternalServerError)
		return
	}
	if sb == nil {
		log.Printf("get sandbox %s - not found", id)
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, sb)
}

func (h *SandboxesHandler) delete(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := h.manager.DeleteSandbox(r.Context(), id); err != nil {
		log.Printf("delete sandbox %s - error: %v", id, err)
		http.Error(w, "failed to delete sandbox", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SandboxesHandler) heartbeat(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := h.manager.Heartbeat(r.Context(), id); err != nil {
		log.Printf("heartbeat sandbox %s - error: %v", id, err)
		http.Error(w, "failed to record heartbeat", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

