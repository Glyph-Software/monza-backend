//go:build linux

package main

import (
	"monza/backend/internal/microvm"
	"monza/backend/internal/microvm/firecracker"
)

func newPlatformRuntime(cfg runtimeConfig) (microvm.Runtime, error) {
	return firecracker.New(firecracker.Config{
		FirecrackerBin: cfg.FirecrackerBin,
		KernelCacheDir: cfg.KernelCacheDir,
		ImageCacheDir:  cfg.ImageCacheDir,
		AgentBinPath:   cfg.AgentBinPath,
		InitScriptPath: cfg.InitScriptPath,
		NetworkSubnet:  cfg.NetworkSubnet,
		RootfsSizeMB:   cfg.RootfsSizeMB,
	})
}
