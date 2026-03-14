package sandbox

import (
	"context"
	"log"
)

// StartProvisionWorker starts a background goroutine that consumes the provision
// queue and runs image pull + container create/start for each sandbox. It stops
// when the context is done.
func (m *Manager) StartProvisionWorker(ctx context.Context) {
	go func() {
		log.Printf("sandbox manager - starting provision worker")
		for {
			select {
			case <-ctx.Done():
				log.Printf("sandbox manager - provision worker stopping (context done)")
				return
			case id, ok := <-m.provisionQueue:
				if !ok {
					return
				}
				m.provisionOne(ctx, id)
			}
		}
	}()
}
