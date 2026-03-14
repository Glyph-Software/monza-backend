package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
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


