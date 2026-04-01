package sandbox

import (
	"context"
	"log"
	"sync"
	"time"
)

const cleanupConcurrency = 5

// CleanupExpired scans for sandboxes whose last activity is older than the
// configured session TTL, destroys their VMs in parallel, then soft-deletes
// the sandbox rows.
func (m *Manager) CleanupExpired(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-m.sessionTTL)

	expired, err := m.sandboxRepo.FindExpired(ctx, cutoff)
	if err != nil {
		log.Printf("sandbox manager - CleanupExpired find error: %v", err)
		return err
	}

	if len(expired) == 0 {
		return nil
	}

	log.Printf("sandbox manager - CleanupExpired found %d expired sandboxes", len(expired))

	sem := make(chan struct{}, cleanupConcurrency)
	var wg sync.WaitGroup

	for _, sb := range expired {
		sb := sb
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			if sb.ContainerID != "" {
				_ = m.runtime.Destroy(ctx, sb.ContainerID)
			}

			now := time.Now().UTC()
			if err := m.sandboxRepo.MarkDeleted(ctx, sb.ID, now); err != nil {
				log.Printf("sandbox manager - CleanupExpired MarkDeleted(%s) error: %v", sb.ID, err)
				return
			}

			log.Printf("sandbox manager - CleanupExpired removed sandbox id=%s handle=%s", sb.ID, sb.ContainerID)
		}()
	}

	wg.Wait()
	return nil
}
