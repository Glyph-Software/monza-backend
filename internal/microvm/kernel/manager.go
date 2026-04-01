package kernel

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

// Well-known kernel URLs from Firecracker's CI artifacts.
const (
	kernelURLAmd64 = "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.11/x86_64/vmlinux-6.1.102"
	kernelURLArm64 = "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.11/aarch64/vmlinux-6.1.102"
)

// Manager handles downloading and caching Linux kernel binaries for microVMs.
type Manager struct {
	cacheDir string
}

// NewManager creates a kernel manager that stores kernels under cacheDir.
func NewManager(cacheDir string) (*Manager, error) {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".monza", "kernels")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{cacheDir: cacheDir}, nil
}

// EnsureKernel returns the path to a cached kernel binary, downloading it on
// first use. The arch parameter should be "amd64" or "arm64"; empty defaults
// to the host architecture.
func (m *Manager) EnsureKernel(arch string) (string, error) {
	if arch == "" {
		arch = runtime.GOARCH
	}

	var url string
	switch arch {
	case "amd64":
		url = kernelURLAmd64
	case "arm64":
		url = kernelURLArm64
	default:
		return "", fmt.Errorf("unsupported kernel arch: %s", arch)
	}

	filename := fmt.Sprintf("vmlinux-%s", arch)
	kernelPath := filepath.Join(m.cacheDir, filename)

	if _, err := os.Stat(kernelPath); err == nil {
		return kernelPath, nil
	}

	log.Printf("kernel manager: downloading kernel for %s from %s", arch, url)

	if err := downloadFile(url, kernelPath); err != nil {
		return "", fmt.Errorf("download kernel: %w", err)
	}

	log.Printf("kernel manager: cached kernel at %s", kernelPath)
	return kernelPath, nil
}

// KernelPath returns the expected path for a kernel without downloading.
func (m *Manager) KernelPath(arch string) string {
	if arch == "" {
		arch = runtime.GOARCH
	}
	return filepath.Join(m.cacheDir, fmt.Sprintf("vmlinux-%s", arch))
}

func downloadFile(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "kernel-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0o644); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}
