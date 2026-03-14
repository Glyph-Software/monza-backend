package sandbox

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"

	"monza/backend/internal/db"
	"monza/backend/internal/db/repository"
	"monza/backend/internal/devcontainer"
	"monza/backend/internal/docker"
	"monza/backend/pkg/models"
)

const defaultProvisionQueueSize = 256

// ErrFileNotFound is returned by DownloadFile when the requested path does not exist in the container.
var ErrFileNotFound = errors.New("file not found")

// ResourceLimits holds CPU and memory limits applied to each sandbox container.
// Zero values mean no limit (Docker default).
type ResourceLimits struct {
	Memory   int64 // memory limit in bytes; 0 = no limit
	NanoCPUs int64 // CPU quota in units of 1e-9 CPU; 0 = no limit (1 CPU = 1e9)
}

// ExecuteResult mirrors the DeepAgents ExecuteResponse type.
type ExecuteResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}

type Manager struct {
	sandboxRepo     *repository.SandboxRepository
	portRepo        *repository.PortRepository
	docker          *docker.Client
	sessionTTL      time.Duration
	provisionQueue  chan uuid.UUID
	resourceLimits  ResourceLimits
	heartbeatBuffer sync.Map // uuid.UUID -> time.Time (in-memory buffer for batching)
	hostID          string
}

func NewManager(database *db.DB, dockerClient *docker.Client, sessionTTL time.Duration, limits ResourceLimits, hostID string) *Manager {
	return &Manager{
		sandboxRepo:     repository.NewSandboxRepository(database, hostID),
		portRepo:        repository.NewPortRepository(database),
		docker:          dockerClient,
		sessionTTL:     sessionTTL,
		provisionQueue: make(chan uuid.UUID, defaultProvisionQueueSize),
		resourceLimits: limits,
		hostID:          hostID,
	}
}

// CreateFromDevcontainer inserts a sandbox record with status "creating" and
// enqueues it for asynchronous provisioning. The actual image pull and
// container create/start are performed by the provision worker; the caller
// receives the sandbox immediately with status creating (202 Accepted).
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

	log.Printf("sandbox manager - enqueueing sandbox name=%q image=%q", name, cfg.Image)

	now := time.Now().UTC()
	sb := &models.Sandbox{
		ID:             uuid.New(),
		Name:           name,
		Status:         models.SandboxStatusCreating,
		Image:          cfg.Image,
		WorkspaceMount: workspaceMount,
		HostID:         m.hostID,
		LastActivity:   now,
		CreatedAt:      now,
	}

	if err := m.sandboxRepo.Insert(ctx, sb); err != nil {
		log.Printf("sandbox manager - sandboxRepo.Insert failed: %v", err)
		return nil, err
	}

	select {
	case m.provisionQueue <- sb.ID:
	default:
		log.Printf("sandbox manager - provision queue full, sandbox id=%s will not be provisioned", sb.ID)
		_ = m.sandboxRepo.UpdateStatus(ctx, sb.ID, models.SandboxStatusError)
		return nil, errors.New("provision queue full")
	}

	return sb, nil
}

// provisionOne performs image pull, container create, and container start for
// a sandbox that is in status "creating". Called by the provision worker.
func (m *Manager) provisionOne(ctx context.Context, id uuid.UUID) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil || sb == nil {
		if err != nil {
			log.Printf("sandbox manager - provisionOne(%s) GetByID error: %v", id, err)
		}
		return
	}
	if sb.Status != models.SandboxStatusCreating {
		return
	}

	if err := m.docker.ImagePull(ctx, sb.Image); err != nil {
		log.Printf("sandbox manager - provisionOne(%s) ImagePull(%q) failed: %v", id, sb.Image, err)
		_ = m.sandboxRepo.UpdateStatus(ctx, id, models.SandboxStatusError)
		return
	}

	containerConfig := &container.Config{
		Image: sb.Image,
		Tty:   true,
		Env:   nil,
	}
	hostConfig := &container.HostConfig{}
	if sb.WorkspaceMount != "" {
		hostConfig.Binds = []string{sb.WorkspaceMount + ":/workspace"}
	}
	if m.resourceLimits.Memory > 0 {
		hostConfig.Resources.Memory = m.resourceLimits.Memory
	}
	if m.resourceLimits.NanoCPUs > 0 {
		hostConfig.Resources.NanoCPUs = m.resourceLimits.NanoCPUs
	}

	createResp, err := m.docker.ContainerCreate(ctx, containerConfig, hostConfig)
	if err != nil {
		log.Printf("sandbox manager - provisionOne(%s) ContainerCreate failed: %v", id, err)
		_ = m.sandboxRepo.UpdateStatus(ctx, id, models.SandboxStatusError)
		return
	}

	if err := m.sandboxRepo.SetContainerReady(ctx, id, createResp.ID); err != nil {
		log.Printf("sandbox manager - provisionOne(%s) SetContainerReady failed: %v", id, err)
		_ = m.docker.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return
	}

	if err := m.docker.ContainerStart(ctx, createResp.ID); err != nil {
		log.Printf("sandbox manager - provisionOne(%s) ContainerStart failed: %v", id, err)
		_ = m.sandboxRepo.UpdateStatus(ctx, id, models.SandboxStatusError)
		_ = m.docker.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return
	}

	log.Printf("sandbox manager - provisioned sandbox id=%s container_id=%s", id, createResp.ID)
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

// UploadFile copies a tar archive (content) into the sandbox's container at
// the given destination path. The content reader must provide a valid tar
// stream, and the caller is responsible for choosing an appropriate path
// (e.g. /workspace).
func (m *Manager) UploadFile(
	ctx context.Context,
	id uuid.UUID,
	dstPath string,
	content io.Reader,
) error {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - UploadFile(%s) GetByID error: %v", id, err)
		return err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - UploadFile(%s) no-op (sandbox or container not found)", id)
		return errors.New("sandbox or container not found")
	}

	if err := m.docker.CopyToContainer(ctx, sb.ContainerID, dstPath, content); err != nil {
		log.Printf("sandbox manager - UploadFile(%s) CopyToContainer error: %v", id, err)
		return err
	}

	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - UploadFile(%s) UpdateLastActivity error: %v", id, err)
	}

	return nil
}

// DownloadFile returns a tar archive stream for the given path inside the
// sandbox's container. The caller is responsible for closing the returned
// ReadCloser and extracting the desired file from the tar stream.
func (m *Manager) DownloadFile(
	ctx context.Context,
	id uuid.UUID,
	srcPath string,
) (io.ReadCloser, error) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) GetByID error: %v", id, err)
		return nil, err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - DownloadFile(%s) no-op (sandbox or container not found)", id)
		return nil, errors.New("sandbox or container not found")
	}

	rc, err := m.docker.CopyFromContainer(ctx, sb.ContainerID, srcPath)
	if err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) CopyFromContainer error: %v", id, err)
		if strings.Contains(err.Error(), "no such file or directory") {
			return nil, ErrFileNotFound
		}
		return nil, err
	}

	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) UpdateLastActivity error: %v", id, err)
	}

	return rc, nil
}

