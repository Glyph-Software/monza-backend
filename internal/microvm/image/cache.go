package image

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Manager handles OCI image pulling, rootfs assembly, and caching. It is safe
// for concurrent use.
type Manager struct {
	cacheDir       string
	agentBinPath   string
	initScriptPath string
	rootfsSizeMB   int

	mu sync.Mutex // serialises builds for the same image
}

// ManagerConfig configures the image manager.
type ManagerConfig struct {
	CacheDir       string // e.g. ~/.monza/images
	AgentBinPath   string // path to cross-compiled monza-agent binary
	InitScriptPath string // path to init script
	RootfsSizeMB   int    // default 2048
}

// NewManager creates a new image manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.CacheDir == "" {
		home, _ := os.UserHomeDir()
		cfg.CacheDir = filepath.Join(home, ".monza", "images")
	}
	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{
		cacheDir:       cfg.CacheDir,
		agentBinPath:   cfg.AgentBinPath,
		initScriptPath: cfg.InitScriptPath,
		rootfsSizeMB:   cfg.RootfsSizeMB,
	}, nil
}

// Prepare ensures a bootable ext4 rootfs exists for the given OCI image. It
// returns the path to a cached, read-only base rootfs. Callers that need a
// writable disk should copy this file before booting.
func (m *Manager) Prepare(ctx context.Context, imageRef string) (rootfsPath string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tarPath, digest, err := PullAndFlatten(ctx, imageRef, m.cacheDir)
	if err != nil {
		return "", fmt.Errorf("pull %q: %w", imageRef, err)
	}

	rootfsDir := filepath.Join(m.cacheDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return "", err
	}

	rootfsPath = filepath.Join(rootfsDir, digest+".ext4")
	if _, err := os.Stat(rootfsPath); err == nil {
		log.Printf("image cache: using cached rootfs %s", rootfsPath)
		return rootfsPath, nil
	}

	if err := BuildRootfs(tarPath, m.agentBinPath, m.initScriptPath, rootfsPath, m.rootfsSizeMB); err != nil {
		os.Remove(rootfsPath)
		return "", fmt.Errorf("build rootfs: %w", err)
	}

	return rootfsPath, nil
}

// CopyRootfs creates a writable copy of the base rootfs for a specific VM
// instance. The copy is stored in a per-sandbox subdirectory.
func (m *Manager) CopyRootfs(baseRootfs string, sandboxName string) (string, error) {
	instanceDir := filepath.Join(m.cacheDir, "instances", sandboxName)
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		return "", err
	}

	dst := filepath.Join(instanceDir, "rootfs.ext4")
	if err := copyFile(baseRootfs, dst, 0o644); err != nil {
		return "", fmt.Errorf("copy rootfs: %w", err)
	}

	return dst, nil
}

// CleanupInstance removes the per-sandbox rootfs copy.
func (m *Manager) CleanupInstance(sandboxName string) {
	instanceDir := filepath.Join(m.cacheDir, "instances", sandboxName)
	if err := os.RemoveAll(instanceDir); err != nil {
		log.Printf("image cache: cleanup %s: %v", sandboxName, err)
	}
}
