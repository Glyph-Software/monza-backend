package microvm

// Agent protocol message types exchanged over vsock as newline-delimited JSON.

const (
	AgentVsockPort uint32 = 1024

	MsgTypeExec      = "exec"
	MsgTypeWriteFile = "write_file"
	MsgTypeReadFile  = "read_file"

	MsgTypeReady    = "ready"
	MsgTypeStdout   = "stdout"
	MsgTypeStderr   = "stderr"
	MsgTypeExit     = "exit"
	MsgTypeOK       = "ok"
	MsgTypeFileData = "file_data"
	MsgTypeError    = "error"
)

// Request is the envelope sent from host to guest agent.
type Request struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Command  string `json:"command,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`

	// File operations
	Path string `json:"path,omitempty"`
	Data string `json:"data,omitempty"` // base64-encoded for file content
	Mode uint32 `json:"mode,omitempty"`
}

// Response is the envelope sent from guest agent to host.
type Response struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Data    string `json:"data,omitempty"`
	Code    int    `json:"code,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Message string `json:"message,omitempty"`
}
