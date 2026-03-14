package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"monza/backend/internal/db"
	"monza/backend/pkg/models"
)

type SandboxRepository struct {
	db *db.DB
}

func NewSandboxRepository(database *db.DB) *SandboxRepository {
	return &SandboxRepository{db: database}
}

func (r *SandboxRepository) Insert(ctx context.Context, s *models.Sandbox) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}

	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}

	if s.LastActivity.IsZero() {
		s.LastActivity = s.CreatedAt
	}

	var envJSON []byte
	if s.EnvVars != nil {
		data, err := json.Marshal(s.EnvVars)
		if err != nil {
			return err
		}
		envJSON = data
	}

	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO sandboxes (
			id, name, status, container_id, image, workspace_mount,
			devcontainer_config, env_vars, last_activity, created_at, expires_at, deleted_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, s.ID, s.Name, s.Status, s.ContainerID, s.Image, s.WorkspaceMount,
		s.DevcontainerConfig, envJSON, s.LastActivity, s.CreatedAt, s.ExpiresAt, s.DeletedAt,
	)
	return err
}

func (r *SandboxRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Sandbox, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, name, status, container_id, image, workspace_mount,
		       devcontainer_config, env_vars, last_activity, created_at, expires_at, deleted_at
		FROM sandboxes
		WHERE id = $1 AND deleted_at IS NULL
	`, id)

	var s models.Sandbox
	var envJSON []byte

	err := row.Scan(
		&s.ID,
		&s.Name,
		&s.Status,
		&s.ContainerID,
		&s.Image,
		&s.WorkspaceMount,
		&s.DevcontainerConfig,
		&envJSON,
		&s.LastActivity,
		&s.CreatedAt,
		&s.ExpiresAt,
		&s.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if len(envJSON) > 0 {
		if err := json.Unmarshal(envJSON, &s.EnvVars); err != nil {
			return nil, err
		}
	}

	return &s, nil
}

func (r *SandboxRepository) ListActive(ctx context.Context) ([]models.Sandbox, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, name, status, container_id, image, workspace_mount,
		       devcontainer_config, env_vars, last_activity, created_at, expires_at, deleted_at
		FROM sandboxes
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]models.Sandbox, 0)

	for rows.Next() {
		var s models.Sandbox
		var envJSON []byte

		if err := rows.Scan(
			&s.ID,
			&s.Name,
			&s.Status,
			&s.ContainerID,
			&s.Image,
			&s.WorkspaceMount,
			&s.DevcontainerConfig,
			&envJSON,
			&s.LastActivity,
			&s.CreatedAt,
			&s.ExpiresAt,
			&s.DeletedAt,
		); err != nil {
			return nil, err
		}

		if len(envJSON) > 0 {
			if err := json.Unmarshal(envJSON, &s.EnvVars); err != nil {
				return nil, err
			}
		}

		results = append(results, s)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return results, nil
}

func (r *SandboxRepository) UpdateLastActivity(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE sandboxes
		SET last_activity = $2
		WHERE id = $1 AND deleted_at IS NULL
	`, id, t)
	return err
}

func (r *SandboxRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.SandboxStatus) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE sandboxes
		SET status = $2
		WHERE id = $1 AND deleted_at IS NULL
	`, id, status)
	return err
}

func (r *SandboxRepository) MarkDeleted(ctx context.Context, id uuid.UUID, deletedAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE sandboxes
		SET deleted_at = $2, status = $3
		WHERE id = $1 AND deleted_at IS NULL
	`, id, deletedAt, models.SandboxStatusDeleted)
	return err
}

func (r *SandboxRepository) MarkExpired(ctx context.Context, id uuid.UUID, expiresAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		UPDATE sandboxes
		SET expires_at = $2, status = $3
		WHERE id = $1 AND deleted_at IS NULL
	`, id, expiresAt, models.SandboxStatusExpired)
	return err
}

// Delete removes the sandbox row by id. port_mappings and activity_logs are
// removed automatically via ON DELETE CASCADE.
func (r *SandboxRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM sandboxes WHERE id = $1`, id)
	return err
}

func (r *SandboxRepository) FindExpired(ctx context.Context, cutoff time.Time) ([]models.Sandbox, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, name, status, container_id, image, workspace_mount,
		       devcontainer_config, env_vars, last_activity, created_at, expires_at, deleted_at
		FROM sandboxes
		WHERE status = 'running'
		  AND deleted_at IS NULL
		  AND last_activity < $1
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.Sandbox

	for rows.Next() {
		var s models.Sandbox
		var envJSON []byte

		if err := rows.Scan(
			&s.ID,
			&s.Name,
			&s.Status,
			&s.ContainerID,
			&s.Image,
			&s.WorkspaceMount,
			&s.DevcontainerConfig,
			&envJSON,
			&s.LastActivity,
			&s.CreatedAt,
			&s.ExpiresAt,
			&s.DeletedAt,
		); err != nil {
			return nil, err
		}

		if len(envJSON) > 0 {
			if err := json.Unmarshal(envJSON, &s.EnvVars); err != nil {
				return nil, err
			}
		}

		results = append(results, s)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return results, nil
}

