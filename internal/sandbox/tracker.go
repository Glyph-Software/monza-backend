package sandbox

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

// Heartbeat records activity for a sandbox and bumps its last_activity
// timestamp. This is intended to be called by the HTTP heartbeat endpoint.
func (m *Manager) Heartbeat(ctx context.Context, id uuid.UUID) error {
	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - Heartbeat(%s) error: %v", id, err)
		return err
	}
	log.Printf("sandbox manager - Heartbeat(%s) recorded", id)
	return nil
}

