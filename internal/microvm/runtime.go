package microvm

import (
	"context"
	"io"
	"os"
)

// Runtime abstracts the VM lifecycle so that the sandbox manager is decoupled
// from any particular hypervisor. Firecracker (Linux) and Apple
// Virtualization.framework (macOS) both implement this interface.
type Runtime interface {
	// Provision pulls/prepares the image, boots a microVM, and waits for the
	// guest agent to become ready. The returned handle identifies the VM for
	// all subsequent operations and is stored in the database as ContainerID.
	Provision(ctx context.Context, opts ProvisionOpts) (handle string, err error)

	// Destroy tears down the VM and cleans up all associated resources
	// (rootfs copy, network devices, etc.).
	Destroy(ctx context.Context, handle string) error

	// Exec runs a shell command inside the VM via the guest agent and returns
	// combined output capped at maxBytes.
	Exec(ctx context.Context, handle string, command string, maxBytes int) (ExecResult, error)

	// WriteFile writes content to guestPath inside the VM.
	WriteFile(ctx context.Context, handle string, guestPath string, content io.Reader, mode os.FileMode) error

	// ReadFile returns the content of guestPath inside the VM. The caller
	// must close the returned ReadCloser. size may be -1 if unknown.
	ReadFile(ctx context.Context, handle string, guestPath string) (rc io.ReadCloser, size int64, err error)

	// Close releases any resources held by the runtime (e.g. cached
	// connections, background goroutines).
	Close() error
}

// ProvisionOpts configures a new microVM.
type ProvisionOpts struct {
	Name      string
	Image     string // OCI image reference, e.g. "python:3.12"
	MemoryMiB int
	VCPUs     int
	EnvVars   map[string]string
}

// ExecResult holds the output of a command executed inside a VM.
type ExecResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}
