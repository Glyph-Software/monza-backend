package repository

import (
	"context"

	"github.com/google/uuid"

	"monza/backend/internal/db"
	"monza/backend/pkg/models"
)

type PortRepository struct {
	db *db.DB
}

func NewPortRepository(database *db.DB) *PortRepository {
	return &PortRepository{db: database}
}

func (r *PortRepository) InsertForSandbox(ctx context.Context, sandboxID uuid.UUID, mappings []models.PortMapping) error {
	batch := &db.Batch{}
	for _, m := range mappings {
		id := m.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		batch.Queue(`
			INSERT INTO port_mappings (id, sandbox_id, host_port, container_port)
			VALUES ($1,$2,$3,$4)
		`, id, sandboxID, m.HostPort, m.ContainerPort)
	}
	return r.db.SendBatch(ctx, batch)
}

func (r *PortRepository) ListBySandboxID(ctx context.Context, sandboxID uuid.UUID) ([]models.PortMapping, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, sandbox_id, host_port, container_port
		FROM port_mappings
		WHERE sandbox_id = $1
	`, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.PortMapping
	for rows.Next() {
		var m models.PortMapping
		if err := rows.Scan(&m.ID, &m.SandboxID, &m.HostPort, &m.ContainerPort); err != nil {
			return nil, err
		}
		results = append(results, m)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return results, nil
}

