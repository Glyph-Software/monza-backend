package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type SandboxStatus string

const (
	SandboxStatusCreating SandboxStatus = "creating"
	SandboxStatusRunning  SandboxStatus = "running"
	SandboxStatusExpired  SandboxStatus = "expired"
	SandboxStatusDeleted  SandboxStatus = "deleted"
	SandboxStatusError    SandboxStatus = "error"
)

type PortMapping struct {
	ID            uuid.UUID `json:"id"`
	SandboxID     uuid.UUID `json:"sandbox_id"`
	HostPort      int       `json:"host_port"`
	ContainerPort int       `json:"container_port"`
}

type Sandbox struct {
	ID                 uuid.UUID            `json:"id"`
	Name               string               `json:"name"`
	Status             SandboxStatus        `json:"status"`
	ContainerID        string               `json:"container_id"`
	Image              string               `json:"image"`
	WorkspaceMount     string               `json:"workspace_mount"`
	DevcontainerConfig json.RawMessage      `json:"devcontainer_config,omitempty"`
	EnvVars            map[string]string    `json:"env_vars,omitempty"`
	PortMappings       []PortMapping        `json:"port_mappings,omitempty"`
	LastActivity       time.Time            `json:"last_activity"`
	CreatedAt          time.Time            `json:"created_at"`
	ExpiresAt          *time.Time           `json:"expires_at,omitempty"`
	DeletedAt          *time.Time           `json:"deleted_at,omitempty"`
}

