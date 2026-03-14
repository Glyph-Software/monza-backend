package sandbox

import (
	"context"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
)

// CleanupExpired scans for sandboxes whose last activity is older than the
// configured session TTL and stops/removes their containers while marking them
// as expired in the database.
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

	for _, sb := range expired {
		if sb.ContainerID != "" {
			_ = m.docker.ContainerStop(ctx, sb.ContainerID, container.StopOptions{})
			_ = m.docker.ContainerRemove(ctx, sb.ContainerID, container.RemoveOptions{Force: true})
		}

		if err := m.sandboxRepo.MarkExpired(ctx, sb.ID, time.Now().UTC()); err != nil {
			log.Printf("sandbox manager - CleanupExpired MarkExpired(%s) error: %v", sb.ID, err)
			return err
		}

		log.Printf("sandbox manager - CleanupExpired expired sandbox id=%s container_id=%s", sb.ID, sb.ContainerID)
	}

	return nil
}

