//go:build linux

package main

import (
	"log"
	"os"
	"syscall"
)

func mountFS() {
	mounts := []struct {
		source string
		target string
		fstype string
	}{
		{"proc", "/proc", "proc"},
		{"sys", "/sys", "sysfs"},
		{"dev", "/dev", "devtmpfs"},
		{"tmp", "/tmp", "tmpfs"},
		{"devpts", "/dev/pts", "devpts"},
	}

	for _, m := range mounts {
		_ = os.MkdirAll(m.target, 0o755)
		if err := syscall.Mount(m.source, m.target, m.fstype, 0, ""); err != nil {
			log.Printf("monza-agent: mount %s on %s: %v (may already be mounted)", m.fstype, m.target, err)
		}
	}
}
