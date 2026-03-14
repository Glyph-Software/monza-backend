package sandbox

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

const heartbeatFlushInterval = 30 * time.Second

// StartHeartbeatFlusher starts a background goroutine that periodically
// flushes buffered heartbeats to the database in batch. It stops when the
// context is done.
func (m *Manager) StartHeartbeatFlusher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(heartbeatFlushInterval)
		defer ticker.Stop()

		log.Printf("sandbox manager - starting heartbeat flusher with interval %s", heartbeatFlushInterval)
		for {
			select {
			case <-ctx.Done():
				log.Printf("sandbox manager - heartbeat flusher stopping (context done)")
				return
			case <-ticker.C:
				m.flushHeartbeats(context.Background())
			}
		}
	}()
}

func (m *Manager) flushHeartbeats(ctx context.Context) {
	updates := make(map[uuid.UUID]time.Time)
	m.heartbeatBuffer.Range(func(k, v interface{}) bool {
		id := k.(uuid.UUID)
		t := v.(time.Time)
		updates[id] = t
		m.heartbeatBuffer.Delete(k)
		return true
	})
	if len(updates) == 0 {
		return
	}
	if err := m.sandboxRepo.BatchUpdateLastActivity(ctx, updates); err != nil {
		log.Printf("sandbox manager - heartbeat flush error: %v", err)
		return
	}
	log.Printf("sandbox manager - flushed %d heartbeats to database", len(updates))
}
