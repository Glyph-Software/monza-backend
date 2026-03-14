package sandbox

import (
	"context"
	"log"
	"time"
)

// StartCleanupWorker starts a background goroutine that periodically calls
// CleanupExpired using the given interval. It stops when the context is done.
func (m *Manager) StartCleanupWorker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		log.Printf("sandbox manager - starting cleanup worker with interval %s", interval)
		for {
			select {
			case <-ctx.Done():
				log.Printf("sandbox manager - cleanup worker stopping (context done)")
				return
			case <-ticker.C:
				if err := m.CleanupExpired(context.Background()); err != nil {
					log.Printf("sandbox manager - cleanup worker error: %v", err)
				}
			}
		}
	}()
}

