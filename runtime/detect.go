package runtime

import (
	"context"
	"fmt"
	"slices"
	"time"
)

const (
	// dockerSocket is the default Docker daemon socket path.
	dockerSocket = "/var/run/docker.sock"

	// RuntimeDocker selects the Docker SDK runtime.
	RuntimeDocker = "docker"
	// RuntimeNerdctl selects the nerdctl CLI runtime via the ctrctl wrapper.
	RuntimeNerdctl = "nerdctl"
	// RuntimeAuto auto-detects the available runtime (Docker SDK preferred, then CLI auto-detection).
	RuntimeAuto = "auto"
)

// Pingable is an optional interface that runtime implementations can satisfy
// to verify daemon connectivity.
type Pingable interface {
	Ping(ctx context.Context) error
}

// DockerRuntime creates a Docker runtime client using the Docker SDK.
type DockerRuntime func() (Runtime, error)

// NerdctlRuntime creates a ctrctl-backed runtime client using the given CLI command.
type NerdctlRuntime func(cli []string) (Runtime, error)

// Detect selects and creates a runtime client based on the preference string.
//
// Preference values:
//   - "docker": use Docker SDK, fail if unavailable
//   - "nerdctl": use nerdctl via the ctrctl CLI wrapper
//   - "auto" or "": auto-detect (Docker SDK preferred, then nerdctl)
//
// nerdctlNamespace is the namespace passed to nerdctl via --namespace.
// It is only applied when the resolved CLI is nerdctl.
//
// When nsenterHost is true, nerdctl CLI invocations are prefixed with
// nsenter -t 1 -m -u -i -n -p -- so they execute inside all host namespaces.
// nerdctl must already be installed on the host.
// This has no effect on the Docker SDK path.
//
// The dockerFn and nerdctlFn factories construct the actual clients,
// keeping this function decoupled from the concrete implementations.
func Detect(preference string, dockerFn DockerRuntime, nerdctlFn NerdctlRuntime, nerdctlNamespace string, nsenterHost bool) (Runtime, error) {
	switch preference {
	case RuntimeDocker:
		return tryDocker(dockerFn)
	case RuntimeNerdctl:
		return tryNerdctl(nerdctlFn, nerdctlNamespace, nsenterHost)
	case RuntimeAuto, "":
		return autoDetect(dockerFn, nerdctlFn, nerdctlNamespace, nsenterHost)
	default:
		return nil, fmt.Errorf("unknown runtime %q: valid values are %q, %q, %q",
			preference, RuntimeDocker, RuntimeNerdctl, RuntimeAuto)
	}
}

func autoDetect(dockerFn DockerRuntime, nerdctlFn NerdctlRuntime, nerdctlNamespace string, nsenterHost bool) (Runtime, error) {
	// Prefer Docker SDK when the socket is available.
	if rt, err := tryDocker(dockerFn); err == nil {
		return rt, nil
	}

	// Fall back to CLI auto-detection (docker > nerdctl).
	if rt, err := tryNerdctl(nerdctlFn, nerdctlNamespace, nsenterHost); err == nil {
		return rt, nil
	}

	return nil, fmt.Errorf("no container runtime found: checked Docker SDK (%s) and CLI auto-detection (docker, nerdctl)", dockerSocket)
}

func tryDocker(dockerFn DockerRuntime) (Runtime, error) {
	rt, err := dockerFn()
	if err != nil {
		return nil, fmt.Errorf("creating docker runtime: %w", err)
	}
	if p, ok := rt.(Pingable); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:mnd // Using a magic number is fine here.
		defer cancel()
		if err := p.Ping(ctx); err != nil {
			_ = rt.Close()
			return nil, fmt.Errorf("docker daemon not responding: %w", err)
		}
	}
	return rt, nil
}

// tryNerdctl will check if nerdctl is available.
func tryNerdctl(nerdctlFn NerdctlRuntime, namespace string, useNsenter bool) (Runtime, error) {
	cli := []string{"nerdctl"}
	if namespace != "" {
		cli = append(cli, "--namespace", namespace)
	}

	// When nsenter mode is enabled, prepend the nsenter prefix so every
	// ctrctl invocation runs inside all host namespaces.
	// nerdctl must already be installed on the host.
	if useNsenter {
		cli = slices.Insert(cli, 0, "nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--")
	}

	rt, err := nerdctlFn(cli)
	if err != nil {
		return nil, fmt.Errorf("creating ctrctl runtime with %v: %w", cli, err)
	}
	if p, ok := rt.(Pingable); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:mnd // Using a magic number is fine here.
		defer cancel()
		if err := p.Ping(ctx); err != nil {
			_ = rt.Close()
			return nil, fmt.Errorf("container CLI %v not responding: %w", cli, err)
		}
	}
	return rt, nil
}
