package runtime

import (
	"context"
	"fmt"
	"os"
	"time"
)

const (
	// dockerSocket is the default Docker daemon socket path.
	dockerSocket = "/var/run/docker.sock"

	// RuntimeDocker selects the Docker SDK runtime.
	RuntimeDocker = "docker"
	// RuntimeContainerd is a backward-compatible alias that uses nerdctl via the ctrctl CLI wrapper.
	RuntimeContainerd = "containerd"
	// RuntimeAuto auto-detects the available runtime (Docker SDK preferred, then CLI auto-detection).
	RuntimeAuto = "auto"
)

// Pingable is an optional interface that runtime implementations can satisfy
// to verify daemon connectivity.
type Pingable interface {
	Ping(ctx context.Context) error
}

// DockerFactory creates a Docker runtime client using the Docker SDK.
type DockerFactory func() (Runtime, error)

// CtrctlFactory creates a ctrctl-backed runtime client using the given CLI command.
type CtrctlFactory func(cli []string) (Runtime, error)

// Detect selects and creates a runtime client based on the preference string.
//
// Preference values:
//   - "docker": use Docker SDK, fail if unavailable
//   - "containerd": alias for nerdctl via the ctrctl CLI wrapper (backward compat)
//   - "auto" or "": auto-detect (Docker SDK preferred, then CLI auto-detection)
//
// nerdctlNamespace is the containerd namespace passed to nerdctl via --namespace.
// It is only applied when the resolved CLI is nerdctl.
//
// The dockerFn and ctrctlFn factories construct the actual clients,
// keeping this function decoupled from the concrete implementations.
func Detect(preference string, dockerFn DockerFactory, ctrctlFn CtrctlFactory, nerdctlNamespace string) (Runtime, error) {
	switch preference {
	case RuntimeDocker:
		return tryDocker(dockerFn)
	case RuntimeContainerd:
		// Backward compat: "containerd" means use ctrctl with nerdctl.
		return tryCtrctl(ctrctlFn, []string{"nerdctl"}, nerdctlNamespace)
	case RuntimeAuto, "":
		return autoDetect(dockerFn, ctrctlFn, nerdctlNamespace)
	default:
		return nil, fmt.Errorf("unknown runtime %q: valid values are %q, %q, %q",
			preference, RuntimeDocker, RuntimeContainerd, RuntimeAuto)
	}
}

func autoDetect(dockerFn DockerFactory, ctrctlFn CtrctlFactory, nerdctlNamespace string) (Runtime, error) {
	// Prefer Docker SDK when the socket is available.
	if socketExists(dockerSocket) {
		rt, err := tryDocker(dockerFn)
		if err == nil {
			return rt, nil
		}
	}

	// DefaultCLIOrder is the probe order when auto-detecting a container CLI.
	defaultCLIOrder := [][]string{
		{"nerdctl"},
	}
	// Fall back to CLI auto-detection (docker > nerdctl).
	for _, cli := range defaultCLIOrder {
		rt, err := tryCtrctl(ctrctlFn, cli, nerdctlNamespace)
		if err == nil {
			return rt, nil
		}
	}

	return nil, fmt.Errorf("no container runtime found: checked Docker SDK (%s) and CLI auto-detection (docker, nerdctl)", dockerSocket)
}

func tryDocker(dockerFn DockerFactory) (Runtime, error) {
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

// tryCtrctl creates a ctrctl runtime with the specified CLI command.
// If the CLI is nerdctl and nerdctlNamespace is non-empty, --namespace is injected.
func tryCtrctl(ctrctlFn CtrctlFactory, cli []string, nerdctlNamespace string) (Runtime, error) {
	if len(cli) > 0 && cli[0] == "nerdctl" && nerdctlNamespace != "" {
		cli = append([]string{cli[0], "--namespace", nerdctlNamespace}, cli[1:]...)
	}
	rt, err := ctrctlFn(cli)
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

func socketExists(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	// Accept both sockets and any file (Docker socket could be a regular file in some setups).
	return fi.Mode()&os.ModeSocket != 0 || !fi.IsDir()
}
