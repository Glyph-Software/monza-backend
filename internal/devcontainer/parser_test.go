package devcontainer

import (
	"strings"
	"testing"
)

func TestParseBasicConfig(t *testing.T) {
	raw := `{"name":"test","image":"example/image:tag","forwardPorts":[8080]}`

	cfg, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if cfg.Name != "test" {
		t.Fatalf("expected name %q, got %q", "test", cfg.Name)
	}
	if cfg.Image != "example/image:tag" {
		t.Fatalf("expected image %q, got %q", "example/image:tag", cfg.Image)
	}
	if len(cfg.ForwardPorts) != 1 || cfg.ForwardPorts[0] != 8080 {
		t.Fatalf("unexpected forwardPorts: %#v", cfg.ForwardPorts)
	}
}

