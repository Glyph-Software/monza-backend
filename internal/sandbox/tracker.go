package sandbox

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Heartbeat records activity for a sandbox by storing the timestamp in an
// in-memory buffer. A background flusher periodically writes buffered
// heartbeats to the database in batch to reduce write load.
func (m *Manager) Heartbeat(ctx context.Context, id uuid.UUID) error {
	m.heartbeatBuffer.Store(id, time.Now().UTC())
	return nil
}

