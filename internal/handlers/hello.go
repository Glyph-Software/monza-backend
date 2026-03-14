package handlers

import "net/http"

type helloResponse struct {
	Message string `json:"message"`
}

func Hello(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := helloResponse{Message: "hello, world"}
	writeJSON(w, http.StatusOK, resp)
}

