package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"monza/backend/internal/db"
	"monza/backend/internal/httpserver"
	"monza/backend/internal/sandbox"
)

func getHostID() string {
	if id := os.Getenv("HOST_ID"); id != "" {
		return id
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "default"
	}
	return hostname
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("no .env file found (optional): %v", err)
	}

	addr := getAddr()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		log.Fatal("DATABASE_URL must be set")
	}

	const (
		maxMigrationAttempts = 10
		migrationBackoff     = 2 * time.Second
	)

	log.Printf("running database migrations...")
	var lastErr error
	for i := 0; i < maxMigrationAttempts; i++ {
		if err := db.RunMigrations(connString); err != nil {
			lastErr = err
			log.Printf("database migrations attempt %d/%d failed: %v", i+1, maxMigrationAttempts, err)
			time.Sleep(migrationBackoff)
			continue
		}
		lastErr = nil
		break
	}

	if lastErr != nil {
		log.Fatalf("failed to run database migrations after %d attempts: %v", maxMigrationAttempts, lastErr)
	}
	log.Printf("database migrations complete")

	database, err := db.New(ctx, connString)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer database.Close()

	rt, err := newPlatformRuntime(getRuntimeConfig())
	if err != nil {
		log.Fatalf("failed to create microvm runtime: %v", err)
	}
	defer rt.Close()

	sessionTTL := 15 * time.Minute
	resourceLimits := getResourceLimits()
	hostID := getHostID()
	log.Printf("host id: %s", hostID)
	manager := sandbox.NewManager(database, rt, sessionTTL, resourceLimits, hostID)

	manager.StartProvisionWorker(ctx)
	manager.StartHeartbeatFlusher(ctx)
	manager.StartCleanupWorker(ctx, time.Minute)

	srv := httpserver.New(addr, manager)

	log.Printf("starting HTTP server on %s", addr)
	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getAddr() string {
	if addr := os.Getenv("SERVER_ADDR"); addr != "" {
		return addr
	}
	return ":8080"
}

type runtimeConfig struct {
	KernelCacheDir string
	ImageCacheDir  string
	AgentBinPath   string
	InitScriptPath string
	RootfsSizeMB   int
	FirecrackerBin string
	NetworkSubnet  string
}

func getRuntimeConfig() runtimeConfig {
	home, _ := os.UserHomeDir()
	monzaDir := filepath.Join(home, ".monza")

	cfg := runtimeConfig{
		KernelCacheDir: envOrDefault("MONZA_KERNEL_DIR", filepath.Join(monzaDir, "kernels")),
		ImageCacheDir:  envOrDefault("MONZA_IMAGE_DIR", filepath.Join(monzaDir, "images")),
		AgentBinPath:   os.Getenv("MONZA_AGENT_BIN"),
		InitScriptPath: os.Getenv("MONZA_INIT_SCRIPT"),
		FirecrackerBin: os.Getenv("FIRECRACKER_BIN"),
		NetworkSubnet:  envOrDefault("MONZA_NETWORK_SUBNET", "172.16.0.0/16"),
	}

	if v := os.Getenv("MONZA_ROOTFS_SIZE_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RootfsSizeMB = n
		}
	}

	return cfg
}

func getResourceLimits() sandbox.ResourceLimits {
	const (
		defaultMemoryMiB = 512
		defaultVCPUs     = 1
	)
	limits := sandbox.ResourceLimits{
		MemoryMiB: defaultMemoryMiB,
		VCPUs:     defaultVCPUs,
	}
	if v := os.Getenv("SANDBOX_MEMORY_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limits.MemoryMiB = n
		}
	}
	if v := os.Getenv("SANDBOX_CPU_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limits.VCPUs = n
		}
	}
	return limits
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
