package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"monza/backend/internal/db"
	"monza/backend/internal/docker"
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

	// Retry migrations so we don't fail hard if Postgres is still starting up.
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

	dockerClient, err := docker.New()
	if err != nil {
		log.Fatalf("failed to create docker client: %v", err)
	}
	defer dockerClient.Close()

	sessionTTL := 15 * time.Minute
	resourceLimits := getResourceLimits()
	hostID := getHostID()
	log.Printf("host id: %s", hostID)
	manager := sandbox.NewManager(database, dockerClient, sessionTTL, resourceLimits, hostID)

	manager.StartProvisionWorker(ctx)
	manager.StartHeartbeatFlusher(ctx)
	// Run cleanup worker to expire idle sandboxes.
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

// getResourceLimits reads SANDBOX_MEMORY_LIMIT (bytes) and SANDBOX_CPU_LIMIT
// (number of CPUs, e.g. 1) from env with defaults 512MB and 1 CPU.
func getResourceLimits() sandbox.ResourceLimits {
	const (
		defaultMemoryBytes = 512 * 1024 * 1024 // 512 MiB
		defaultNanoCPUs    = 1e9                // 1 CPU
	)
	limits := sandbox.ResourceLimits{
		Memory:   defaultMemoryBytes,
		NanoCPUs: defaultNanoCPUs,
	}
	if v := os.Getenv("SANDBOX_MEMORY_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			limits.Memory = n
		}
	}
	if v := os.Getenv("SANDBOX_CPU_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			limits.NanoCPUs = n * 1e9
		}
	}
	return limits
}

