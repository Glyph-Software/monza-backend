package devcontainer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Parse reads a devcontainer.json configuration from an io.Reader.
func Parse(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read devcontainer config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal devcontainer config: %w", err)
	}

	return &cfg, nil
}

// ParseFile reads and parses a devcontainer.json from the given file path.
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open devcontainer config %q: %w", path, err)
	}
	defer f.Close()

	return Parse(f)
}

