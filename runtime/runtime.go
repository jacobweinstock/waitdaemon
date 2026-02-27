// Package runtime provides an abstraction over container runtimes (Docker, containerd).
package runtime //nolint:revive // this name is fine.

import "context"

// ContainerInfo holds runtime-agnostic container configuration.
// It is used to inspect the current container and to create new containers.
type ContainerInfo struct {
	// Image is the container image reference.
	Image string
	// Env is the list of environment variables in "KEY=VALUE" format.
	Env []string
	// Cmd is the command to run in the container.
	Cmd []string
	// Tty indicates whether a TTY is allocated.
	Tty bool
	// AttachStdout indicates whether stdout is attached.
	AttachStdout bool
	// AttachStderr indicates whether stderr is attached.
	AttachStderr bool
	// Privileged indicates whether the container runs in privileged mode.
	Privileged bool
	// Binds is the list of volume bind mounts in "host:container" format.
	Binds []string
	// PidMode is the PID namespace mode (e.g., "host").
	PidMode string
	// Snapshotter is the containerd snapshotter name (e.g., "overlayfs"). Only used by containerd runtime.
	Snapshotter string
}

// Runtime is the interface that container runtimes must implement.
type Runtime interface {
	// InspectSelf returns the container configuration for the current container.
	// The runtime is responsible for detecting which container it is running in.
	InspectSelf(ctx context.Context) (ContainerInfo, error)
	// RunContainer creates and starts a new container with the given configuration.
	RunContainer(ctx context.Context, info ContainerInfo) error
	// ImageExists checks if the given image reference exists locally.
	ImageExists(ctx context.Context, imageRef string) bool
	// PullImage pulls the given image reference from a registry.
	PullImage(ctx context.Context, imageRef string) error
	// Close cleans up the runtime client resources.
	Close() error
}
