package sandbox

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"

	"monza/backend/internal/db"
	"monza/backend/internal/db/repository"
	"monza/backend/internal/devcontainer"
	"monza/backend/internal/docker"
	"monza/backend/pkg/models"
)

// ExecuteResult mirrors the DeepAgents ExecuteResponse type.
type ExecuteResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}

type Manager struct {
	sandboxRepo *repository.SandboxRepository
	portRepo    *repository.PortRepository
	docker      *docker.Client
	sessionTTL  time.Duration
}

func NewManager(database *db.DB, dockerClient *docker.Client, sessionTTL time.Duration) *Manager {
	return &Manager{
		sandboxRepo: repository.NewSandboxRepository(database),
		portRepo:    repository.NewPortRepository(database),
		docker:      dockerClient,
		sessionTTL:  sessionTTL,
	}
}

// CreateFromDevcontainer creates a new sandbox from a parsed devcontainer.json
// configuration and starts the underlying Docker container.
func (m *Manager) CreateFromDevcontainer(
	ctx context.Context,
	name string,
	workspaceMount string,
	cfg *devcontainer.Config,
) (*models.Sandbox, error) {
	if cfg == nil {
		return nil, errors.New("devcontainer config is required")
	}
	if cfg.Image == "" {
		return nil, errors.New("devcontainer image is required")
	}

	log.Printf("sandbox manager - creating sandbox name=%q image=%q", name, cfg.Image)

	if err := m.docker.ImagePull(ctx, cfg.Image); err != nil {
		log.Printf("sandbox manager - ImagePull(%q) failed: %v", cfg.Image, err)
		return nil, err
	}

	now := time.Now().UTC()

	sb := &models.Sandbox{
		ID:             uuid.New(),
		Name:           name,
		Status:         models.SandboxStatusCreating,
		Image:          cfg.Image,
		WorkspaceMount: workspaceMount,
		LastActivity:   now,
		CreatedAt:      now,
	}

	containerConfig := &container.Config{
		Image: cfg.Image,
		Tty:   true,
		Env:   nil,
	}

	hostConfig := &container.HostConfig{}
	if workspaceMount != "" {
		hostConfig.Binds = []string{workspaceMount + ":/workspace"}
	}

	createResp, err := m.docker.ContainerCreate(ctx, containerConfig, hostConfig)
	if err != nil {
		log.Printf("sandbox manager - ContainerCreate failed: %v", err)
		return nil, err
	}

	sb.ContainerID = createResp.ID
	sb.Status = models.SandboxStatusRunning

	if err := m.sandboxRepo.Insert(ctx, sb); err != nil {
		log.Printf("sandbox manager - sandboxRepo.Insert failed: %v", err)
		return nil, err
	}

	if err := m.docker.ContainerStart(ctx, sb.ContainerID); err != nil {
		log.Printf("sandbox manager - ContainerStart(%s) failed: %v", sb.ContainerID, err)
		return nil, err
	}

	log.Printf("sandbox manager - created sandbox id=%s container_id=%s", sb.ID, sb.ContainerID)
	return sb, nil
}

func (m *Manager) GetSandbox(ctx context.Context, id uuid.UUID) (*models.Sandbox, error) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil || sb == nil {
		if err != nil {
			log.Printf("sandbox manager - GetSandbox(%s) error: %v", id, err)
		}
		return sb, err
	}

	ports, err := m.portRepo.ListBySandboxID(ctx, id)
	if err != nil {
		return nil, err
	}
	sb.PortMappings = ports

	return sb, nil
}

func (m *Manager) ListSandboxes(ctx context.Context) ([]models.Sandbox, error) {
	sandboxes, err := m.sandboxRepo.ListActive(ctx)
	if err != nil {
		log.Printf("sandbox manager - ListSandboxes error: %v", err)
		return nil, err
	}

	for i := range sandboxes {
		ports, err := m.portRepo.ListBySandboxID(ctx, sandboxes[i].ID)
		if err != nil {
			return nil, err
		}
		sandboxes[i].PortMappings = ports
	}

	log.Printf("sandbox manager - ListSandboxes returned %d sandboxes", len(sandboxes))
	return sandboxes, nil
}

// DeleteSandbox stops and removes the backing container (if any) and marks the
// sandbox as deleted in the database.
func (m *Manager) DeleteSandbox(ctx context.Context, id uuid.UUID) error {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - DeleteSandbox(%s) GetByID error: %v", id, err)
		return err
	}
	if sb == nil {
		log.Printf("sandbox manager - DeleteSandbox(%s) no-op (not found)", id)
		return nil
	}

	if sb.ContainerID != "" {
		_ = m.docker.ContainerStop(ctx, sb.ContainerID, container.StopOptions{})
		_ = m.docker.ContainerRemove(ctx, sb.ContainerID, container.RemoveOptions{Force: true})
	}

	if err := m.sandboxRepo.MarkDeleted(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - DeleteSandbox(%s) MarkDeleted error: %v", id, err)
		return err
	}

	log.Printf("sandbox manager - DeleteSandbox(%s) completed", id)
	return nil
}

// Execute runs a shell command inside the sandbox's Docker container and returns
// a DeepAgents-compatible ExecuteResult. Output is capped at maxOutputBytes.
func (m *Manager) Execute(ctx context.Context, id uuid.UUID, command string, maxOutputBytes int) (*ExecuteResult, error) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - Execute(%s) GetByID error: %v", id, err)
		return nil, err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - Execute(%s) no-op (sandbox or container not found)", id)
		return &ExecuteResult{
			Output:    "sandbox or container not found",
			ExitCode:  1,
			Truncated: false,
		}, nil
	}

	res, err := m.docker.Exec(ctx, sb.ContainerID, command, maxOutputBytes)
	if err != nil {
		log.Printf("sandbox manager - Exec(%s) command %q error: %v", id, command, err)
		return nil, err
	}

	// Update last activity timestamp on successful execution.
	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - Execute(%s) UpdateLastActivity error: %v", id, err)
	}

	return &ExecuteResult{
		Output:    res.Output,
		ExitCode:  res.ExitCode,
		Truncated: res.Truncated,
	}, nil
}

