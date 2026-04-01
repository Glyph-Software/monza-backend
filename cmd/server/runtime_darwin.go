//go:build darwin

package main

import (
	"monza/backend/internal/microvm"
	"monza/backend/internal/microvm/applevm"
)

func newPlatformRuntime(cfg runtimeConfig) (microvm.Runtime, error) {
	return applevm.New(applevm.Config{
		KernelCacheDir: cfg.KernelCacheDir,
		ImageCacheDir:  cfg.ImageCacheDir,
		AgentBinPath:   cfg.AgentBinPath,
		InitScriptPath: cfg.InitScriptPath,
		RootfsSizeMB:   cfg.RootfsSizeMB,
	})
}
