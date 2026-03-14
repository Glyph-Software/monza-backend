package main

import (
	"context"
	"log"
	"os"
	"time"

	"monza/backend/internal/db"
	"monza/backend/internal/docker"
	"monza/backend/internal/httpserver"
	"monza/backend/internal/sandbox"
)

func main() {
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
	manager := sandbox.NewManager(database, dockerClient, sessionTTL)

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

