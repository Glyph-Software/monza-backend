package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func handleWriteFile(req request, send func(response)) {
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		send(response{Type: msgError, ID: req.ID, Message: "base64 decode: " + err.Error()})
		return
	}

	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		send(response{Type: msgError, ID: req.ID, Message: "mkdir: " + err.Error()})
		return
	}

	mode := os.FileMode(req.Mode)
	if mode == 0 {
		mode = 0o644
	}

	if err := os.WriteFile(req.Path, data, mode); err != nil {
		send(response{Type: msgError, ID: req.ID, Message: "write: " + err.Error()})
		return
	}

	send(response{Type: msgOK, ID: req.ID})
}

func handleReadFile(req request, send func(response)) {
	f, err := os.Open(req.Path)
	if err != nil {
		send(response{Type: msgError, ID: req.ID, Message: fmt.Sprintf("open: %v", err)})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		send(response{Type: msgError, ID: req.ID, Message: fmt.Sprintf("stat: %v", err)})
		return
	}

	data, err := io.ReadAll(f)
	if err != nil {
		send(response{Type: msgError, ID: req.ID, Message: fmt.Sprintf("read: %v", err)})
		return
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	send(response{Type: msgFileData, ID: req.ID, Data: encoded, Size: info.Size()})
}
