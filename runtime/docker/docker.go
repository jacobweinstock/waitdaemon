// Package docker implements the runtime.Runtime interface using the Docker Engine API.
package docker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/jacobweinstock/waitdaemon/runtime"
)

// Docker implements runtime.Runtime using the Docker Engine API.
type Docker struct {
	client *client.Client
}

// New creates a new Docker runtime client.
// It uses environment variables (DOCKER_HOST, etc.) and API version negotiation.
func New() (*Docker, error) {
	cl, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Docker{client: cl}, nil
}

// Ping checks if the Docker daemon is responsive.
func (d *Docker) Ping(ctx context.Context) error {
	_, err := d.client.Ping(ctx)
	return err
}

// InspectSelf returns the container configuration for the current container.
// It uses os.Hostname() to get the container ID (Docker sets HOSTNAME to the container short ID).
func (d *Docker) InspectSelf(ctx context.Context) (runtime.ContainerInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("getting hostname: %w", err)
	}
	con, err := d.client.ContainerInspect(ctx, hostname)
	if err != nil {
		return runtime.ContainerInfo{}, err
	}
	return containerInfoFromInspect(con), nil
}

// RunContainer creates and starts a new container with the given configuration.
func (d *Docker) RunContainer(ctx context.Context, info runtime.ContainerInfo) error {
	config := &container.Config{
		Image:        info.Image,
		AttachStdout: info.AttachStdout,
		AttachStderr: info.AttachStderr,
		Cmd:          info.Cmd,
		Tty:          info.Tty,
		Env:          info.Env,
	}

	hostConfig := &container.HostConfig{
		Privileged: info.Privileged,
		Binds:      info.Binds,
		PidMode:    container.PidMode(info.PidMode),
	}

	c, err := d.client.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return err
	}

	return d.client.ContainerStart(ctx, c.ID, container.StartOptions{})
}

// ImageExists reports whether the given image reference exists locally.
func (d *Docker) ImageExists(ctx context.Context, imageRef string) bool {
	_, err := d.client.ImageInspect(ctx, imageRef)
	if err != nil {
		// Any error means the image is not available locally (or the daemon is unreachable).
		return false
	}
	return true
}

// PullImage pulls the given image reference from a registry.
func (d *Docker) PullImage(ctx context.Context, imageRef string) error {
	out, err := d.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(os.Stdout, out)
	return err
}

// Close cleans up the Docker client resources.
func (d *Docker) Close() error {
	return d.client.Close()
}

// containerInfoFromInspect converts a Docker InspectResponse to a runtime.ContainerInfo.
func containerInfoFromInspect(con container.InspectResponse) runtime.ContainerInfo {
	return runtime.ContainerInfo{
		Image:        con.Config.Image,
		Env:          con.Config.Env,
		Cmd:          con.Config.Cmd,
		Tty:          con.Config.Tty,
		AttachStdout: con.Config.AttachStdout,
		AttachStderr: con.Config.AttachStderr,
		Privileged:   con.HostConfig.Privileged,
		Binds:        con.HostConfig.Binds,
		PidMode:      string(con.HostConfig.PidMode),
	}
}
