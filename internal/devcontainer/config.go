package devcontainer

// Config represents a subset of the devcontainer.json specification that is
// relevant for creating a sandbox container.
type Config struct {
	Name              string                         `json:"name"`
	Image             string                         `json:"image"`
	Features          map[string]map[string]any      `json:"features,omitempty"`
	PostCreateCommand string                         `json:"postCreateCommand,omitempty"`
	ForwardPorts      []int                          `json:"forwardPorts,omitempty"`
	Customizations    *Customizations                `json:"customizations,omitempty"`
}

type Customizations struct {
	VSCode *VSCodeCustomizations `json:"vscode,omitempty"`
}

type VSCodeCustomizations struct {
	Extensions []string `json:"extensions,omitempty"`
}

