package sandbox

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"monza/backend/internal/db"
	"monza/backend/internal/db/repository"
	"monza/backend/internal/devcontainer"
	"monza/backend/internal/microvm"
	"monza/backend/pkg/models"
)

const defaultProvisionQueueSize = 256

// ErrFileNotFound is returned by DownloadFile when the requested path does not exist.
var ErrFileNotFound = errors.New("file not found")

// ResourceLimits holds CPU and memory limits applied to each sandbox VM.
type ResourceLimits struct {
	MemoryMiB int // memory in MiB; 0 = runtime default
	VCPUs     int // number of virtual CPUs; 0 = runtime default
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
	runtime         microvm.Runtime
	sessionTTL      time.Duration
	provisionQueue  chan uuid.UUID
	resourceLimits  ResourceLimits
	heartbeatBuffer sync.Map // uuid.UUID -> time.Time (in-memory buffer for batching)
	hostID          string
}

func NewManager(database *db.DB, rt microvm.Runtime, sessionTTL time.Duration, limits ResourceLimits, hostID string) *Manager {
	return &Manager{
		sandboxRepo:    repository.NewSandboxRepository(database, hostID),
		portRepo:       repository.NewPortRepository(database),
		runtime:        rt,
		sessionTTL:     sessionTTL,
		provisionQueue: make(chan uuid.UUID, defaultProvisionQueueSize),
		resourceLimits: limits,
		hostID:         hostID,
	}
}

// CreateFromDevcontainer inserts a sandbox record with status "creating" and
// enqueues it for asynchronous provisioning. The actual image pull and VM boot
// are performed by the provision worker; the caller receives the sandbox
// immediately with status creating (202 Accepted).
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

// provisionOne boots a microVM for a sandbox in status "creating".
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

	handle, err := m.runtime.Provision(ctx, microvm.ProvisionOpts{
		Name:      sb.Name + "-" + id.String()[:8],
		Image:     sb.Image,
		MemoryMiB: m.resourceLimits.MemoryMiB,
		VCPUs:     m.resourceLimits.VCPUs,
		EnvVars:   sb.EnvVars,
	})
	if err != nil {
		log.Printf("sandbox manager - provisionOne(%s) Provision failed: %v", id, err)
		_ = m.sandboxRepo.UpdateStatus(ctx, id, models.SandboxStatusError)
		return
	}

	if err := m.sandboxRepo.SetContainerReady(ctx, id, handle); err != nil {
		log.Printf("sandbox manager - provisionOne(%s) SetContainerReady failed: %v", id, err)
		_ = m.runtime.Destroy(ctx, handle)
		return
	}

	log.Printf("sandbox manager - provisioned sandbox id=%s handle=%s", id, handle)
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

// DeleteSandbox destroys the VM and marks the sandbox as deleted.
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
		_ = m.runtime.Destroy(ctx, sb.ContainerID)
	}

	if err := m.sandboxRepo.MarkDeleted(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - DeleteSandbox(%s) MarkDeleted error: %v", id, err)
		return err
	}

	log.Printf("sandbox manager - DeleteSandbox(%s) completed", id)
	return nil
}

// Execute runs a shell command inside the sandbox's VM and returns an
// ExecuteResult. Output is capped at maxOutputBytes.
func (m *Manager) Execute(ctx context.Context, id uuid.UUID, command string, maxOutputBytes int) (*ExecuteResult, error) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - Execute(%s) GetByID error: %v", id, err)
		return nil, err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - Execute(%s) no-op (sandbox or VM not found)", id)
		return &ExecuteResult{
			Output:    "sandbox or VM not found",
			ExitCode:  1,
			Truncated: false,
		}, nil
	}

	res, err := m.runtime.Exec(ctx, sb.ContainerID, command, maxOutputBytes)
	if err != nil {
		log.Printf("sandbox manager - Exec(%s) command %q error: %v", id, command, err)
		return nil, err
	}

	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - Execute(%s) UpdateLastActivity error: %v", id, err)
	}

	return &ExecuteResult{
		Output:    res.Output,
		ExitCode:  res.ExitCode,
		Truncated: res.Truncated,
	}, nil
}

// UploadFile writes content to a path inside the sandbox VM.
func (m *Manager) UploadFile(
	ctx context.Context,
	id uuid.UUID,
	dstPath string,
	filename string,
	content io.Reader,
) error {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - UploadFile(%s) GetByID error: %v", id, err)
		return err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - UploadFile(%s) no-op (sandbox or VM not found)", id)
		return errors.New("sandbox or VM not found")
	}

	fullPath := dstPath
	if filename != "" {
		if !strings.HasSuffix(fullPath, "/") {
			fullPath += "/"
		}
		fullPath += filename
	}

	if err := m.runtime.WriteFile(ctx, sb.ContainerID, fullPath, content, 0o644); err != nil {
		log.Printf("sandbox manager - UploadFile(%s) WriteFile error: %v", id, err)
		return err
	}

	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - UploadFile(%s) UpdateLastActivity error: %v", id, err)
	}

	return nil
}

// DownloadFile returns a reader for a file inside the sandbox VM.
func (m *Manager) DownloadFile(
	ctx context.Context,
	id uuid.UUID,
	srcPath string,
) (io.ReadCloser, int64, error) {
	sb, err := m.sandboxRepo.GetByID(ctx, id)
	if err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) GetByID error: %v", id, err)
		return nil, 0, err
	}
	if sb == nil || sb.ContainerID == "" {
		log.Printf("sandbox manager - DownloadFile(%s) no-op (sandbox or VM not found)", id)
		return nil, 0, errors.New("sandbox or VM not found")
	}

	rc, size, err := m.runtime.ReadFile(ctx, sb.ContainerID, srcPath)
	if err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) ReadFile error: %v", id, err)
		if strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not found") {
			return nil, 0, ErrFileNotFound
		}
		return nil, 0, err
	}

	if err := m.sandboxRepo.UpdateLastActivity(ctx, id, time.Now().UTC()); err != nil {
		log.Printf("sandbox manager - DownloadFile(%s) UpdateLastActivity error: %v", id, err)
	}

	return rc, size, nil
}

// RuntimeClose shuts down the underlying VM runtime.
func (m *Manager) RuntimeClose() error {
	if m.runtime != nil {
		return m.runtime.Close()
	}
	return nil
}
