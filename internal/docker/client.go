package docker

import (
	"bytes"
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	c, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	return &Client{cli: c}, nil
}

func (c *Client) Close() error {
	return c.cli.Close()
}

// ImagePull pulls the given image reference. Call before ContainerCreate if the image may not exist.
func (c *Client) ImagePull(ctx context.Context, imageRef string) error {
	rc, err := c.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// ContainerCreate is a thin wrapper over the Docker SDK to allow higher-level
// packages to depend on this client instead of the SDK directly.
func (c *Client) ContainerCreate(
	ctx context.Context,
	config *container.Config,
	hostConfig *container.HostConfig,
) (container.CreateResponse, error) {
	return c.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
}

// ContainerStart starts an existing container by ID.
func (c *Client) ContainerStart(ctx context.Context, containerID string) error {
	return c.cli.ContainerStart(ctx, containerID, container.StartOptions{})
}

// ContainerStop stops a running container by ID.
func (c *Client) ContainerStop(ctx context.Context, containerID string, opts container.StopOptions) error {
	return c.cli.ContainerStop(ctx, containerID, opts)
}

// ContainerRemove removes a container by ID.
func (c *Client) ContainerRemove(ctx context.Context, containerID string, opts container.RemoveOptions) error {
	return c.cli.ContainerRemove(ctx, containerID, opts)
}

// ExecResult is a simplified representation of a command executed inside
// a container. It is intentionally similar to DeepAgents' ExecuteResponse.
type ExecResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}

// Exec runs a command inside the given container using /bin/sh -lc so that
// shell features (pipes, redirects, etc.) are available.
//
// The combined stdout/stderr is captured up to maxBytes. If the command
// produces more output, the excess is discarded and Truncated is set to true.
func (c *Client) Exec(
	ctx context.Context,
	containerID string,
	command string,
	maxBytes int,
) (ExecResult, error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	// Use Docker SDK exec APIs so this works with Podman
	// and other Docker-compatible daemons without requiring
	// a docker CLI binary in PATH.
	execOpts := container.ExecOptions{
		Tty:          false,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/sh", "-lc", command},
	}

	createResp, err := c.cli.ContainerExecCreate(ctx, containerID, execOpts)
	if err != nil {
		return ExecResult{}, err
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, createResp.ID, container.ExecAttachOptions{
		Tty: false,
	})
	if err != nil {
		return ExecResult{}, err
	}
	defer attachResp.Close()

	// Read a bounded amount of the raw multiplexed stream from Docker.
	rawLimit := int64(maxBytes*4 + 1) // allow some headroom before truncation
	raw, err := io.ReadAll(io.LimitReader(attachResp.Reader, rawLimit))
	if err != nil {
		return ExecResult{}, err
	}

	// Demultiplex stdout/stderr stream; Docker prefixes each frame with an
	// 8-byte header when TTY=false. stdcopy strips these headers.
	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, bytes.NewReader(raw)); err != nil {
		return ExecResult{}, err
	}

	combined := append(stdoutBuf.Bytes(), stderrBuf.Bytes()...)
	truncated := false
	if len(combined) > maxBytes {
		combined = combined[:maxBytes]
		truncated = true
	}

	inspect, err := c.cli.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return ExecResult{}, err
	}

	exitCode := inspect.ExitCode

	return ExecResult{
		Output:    string(combined),
		ExitCode:  exitCode,
		Truncated: truncated,
	}, nil
}

