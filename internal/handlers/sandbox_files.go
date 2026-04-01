package handlers

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"monza/backend/internal/sandbox"
)

// FilesHandler exposes file upload and download endpoints for a sandbox.
//
// Routes:
//
//	POST /api/sandboxes/{id}/files/upload
//	GET  /api/sandboxes/{id}/files/download?path=/workspace/main.go
type FilesHandler struct {
	manager *sandbox.Manager
}

func NewFilesHandler(m *sandbox.Manager) *FilesHandler {
	return &FilesHandler{manager: m}
}

const (
	maxUploadBytes = 100 * 1024 * 1024 // 100 MiB
)

// HandleUpload uploads a single file into the sandbox VM.
// The request must be multipart/form-data with:
//   - field "file": the file to upload
//   - optional field "path": destination directory inside the VM (default: /workspace)
func (h *FilesHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("sandbox files upload %s - invalid multipart form: %v", id, err)
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	dstPath := r.FormValue("path")
	if dstPath == "" {
		dstPath = "/workspace"
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		if err == http.ErrMissingFile {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		log.Printf("sandbox files upload %s - FormFile error: %v", id, err)
		http.Error(w, "failed to read uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := sanitizeFilename(header)
	if filename == "" {
		http.Error(w, "invalid file name", http.StatusBadRequest)
		return
	}

	log.Printf("HTTP %s %s - uploading file %q to sandbox %s at %q", r.Method, r.URL.Path, filename, id, dstPath)

	if err := h.manager.UploadFile(r.Context(), id, dstPath, filename, file); err != nil {
		log.Printf("sandbox files upload %s - UploadFile error: %v", id, err)
		http.Error(w, "failed to upload file to sandbox", http.StatusInternalServerError)
		return
	}

	fullPath := joinContainerPath(dstPath, filename)

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"path":   fullPath,
	})
}

// HandleDownload streams a single file from the sandbox VM to the client.
// The request must provide a "path" query parameter indicating the full path
// inside the VM (e.g. /workspace/main.go).
func (h *FilesHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	srcPath := r.URL.Query().Get("path")
	if srcPath == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}

	log.Printf("HTTP %s %s - downloading file %q from sandbox %s", r.Method, r.URL.Path, srcPath, id)

	rc, size, err := h.manager.DownloadFile(r.Context(), id, srcPath)
	if err != nil {
		if errors.Is(err, sandbox.ErrFileNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("sandbox files download %s - DownloadFile error: %v", id, err)
		http.Error(w, "failed to download file from sandbox", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	filename := filepath.Base(srcPath)
	if filename == "" || filename == "." {
		filename = "file"
	}

	peek := make([]byte, 512)
	n, readErr := io.ReadFull(rc, peek)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		log.Printf("sandbox files download %s - ReadFull error: %v", id, readErr)
		http.Error(w, "failed to read file content", http.StatusInternalServerError)
		return
	}

	contentType := "application/octet-stream"
	if n > 0 {
		contentType = http.DetectContentType(peek[:n])
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	w.WriteHeader(http.StatusOK)

	if n > 0 {
		if _, err := w.Write(peek[:n]); err != nil {
			log.Printf("sandbox files download %s - error writing initial bytes: %v", id, err)
			return
		}
	}

	if _, err := io.Copy(w, rc); err != nil {
		log.Printf("sandbox files download %s - error streaming file: %v", id, err)
		return
	}
}

func sanitizeFilename(hdr *multipart.FileHeader) string {
	name := hdr.Filename
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	if len(name) > 255 {
		return name[:255]
	}
	return name
}

func joinContainerPath(dir, name string) string {
	if dir == "" {
		return "/" + name
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir + name
}
