package main

// Message type constants mirroring the host-side protocol definitions.
const (
	msgExec      = "exec"
	msgWriteFile = "write_file"
	msgReadFile  = "read_file"

	msgReady    = "ready"
	msgStdout   = "stdout"
	msgStderr   = "stderr"
	msgExit     = "exit"
	msgOK       = "ok"
	msgFileData = "file_data"
	msgError    = "error"
)

type request struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Command  string `json:"command,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
	Path     string `json:"path,omitempty"`
	Data     string `json:"data,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`
}

type response struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Data    string `json:"data,omitempty"`
	Code    int    `json:"code,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Message string `json:"message,omitempty"`
}
