package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"

	"monza/backend/internal/sandbox"
)

// ExecuteHandler exposes a DeepAgents-compatible execute endpoint for a sandbox.
//
// Route shape:
//   POST /api/sandboxes/{id}/execute
//
// Request body:
//   {
//     "command": "echo hello",
//     "max_output_bytes": 65536 // optional hard cap for output size
//   }
//
// Response body (ExecuteResponse):
//   {
//     "output": "hello\n",
//     "exitCode": 0,
//     "truncated": false
//   }
//
// This mirrors the ExecuteResponse type used by DeepAgents sandboxes
// (langchain_daytona, langchain_modal, etc.).
type ExecuteHandler struct {
	manager *sandbox.Manager
}

func NewExecuteHandler(m *sandbox.Manager) *ExecuteHandler {
	return &ExecuteHandler{manager: m}
}

type executeRequest struct {
	Command        string `json:"command"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
}

type executeResponse struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

// HandleExecute dispatches execute requests for a given sandbox.
// It is mounted under /api/sandboxes/ and expects the full path
// to be /api/sandboxes/{id}/execute.
func (h *ExecuteHandler) HandleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The SandboxesHandler already validated and parsed the ID for other
	// sub-routes, but we register this handler separately under its own path
	// for clarity.
	idStr := r.PathValue("id")
	if idStr == "" {
		http.NotFound(w, r)
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		log.Printf("HTTP %s %s - invalid sandbox id %q: %v", r.Method, r.URL.Path, idStr, err)
		http.Error(w, "invalid sandbox id", http.StatusBadRequest)
		return
	}

	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("sandbox execute %s - invalid request body: %v", id, err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	maxBytes := req.MaxOutputBytes
	if maxBytes <= 0 {
		// 64 KiB default cap, consistent with other sandbox providers.
		maxBytes = 64 * 1024
	}

	log.Printf("HTTP %s %s - execute in sandbox %s: %q", r.Method, r.URL.Path, id, req.Command)

	result, err := h.manager.Execute(r.Context(), id, req.Command, maxBytes)
	if err != nil {
		log.Printf("sandbox execute %s - error: %v", id, err)
		http.Error(w, "failed to execute command", http.StatusInternalServerError)
		return
	}

	resp := executeResponse{
		Output:    result.Output,
		ExitCode:  result.ExitCode,
		Truncated: result.Truncated,
	}

	writeJSON(w, http.StatusOK, resp)
}

