// Package nerdctl implements the runtime.Runtime interface using the ctrctl CLI wrapper.
// This supports the nerdctl CLI.
package nerdctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jacobweinstock/waitdaemon/runtime"
	"lesiw.io/ctrctl"
)

// Nerdctl implements runtime.Runtime by shelling out to a container CLI.
type Nerdctl struct {
	cli []string
}

// New creates a Ctrctl runtime using the specified CLI command.
// cli is the command prefix, e.g. []string{"nerdctl"} or []string{"docker"}.
func New(cli []string) (*Nerdctl, error) {
	ctrctl.Cli = cli
	return &Nerdctl{cli: cli}, nil
}

// Ping verifies the CLI is available and responsive.
func (c *Nerdctl) Ping(_ context.Context) error {
	_, err := ctrctl.Version(nil)
	return err
}

// inspectResponse is the subset of the JSON returned by `<cli> container inspect`.
// This is compatible across docker and nerdctl.
type inspectResponse struct {
	// Path is the top-level process binary path (e.g. the resolved entrypoint).
	// nerdctl reliably populates this even when Config.Cmd is empty.
	Path string `json:"Path"`
	// Args is the top-level process arguments (everything after Path).
	// For a single-element entrypoint, Args equals the CMD portion.
	Args []string `json:"Args"`

	Mounts []mountEntry `json:"Mounts"`
	Config struct {
		Image        string   `json:"Image"`
		Env          []string `json:"Env"`
		Cmd          []string `json:"Cmd"`
		Entrypoint   []string `json:"Entrypoint"`
		Tty          bool     `json:"Tty"`
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
	} `json:"Config"`
	HostConfig struct {
		Privileged bool     `json:"Privileged"`
		Binds      []string `json:"Binds"`
		PidMode    string   `json:"PidMode"`
	} `json:"HostConfig"`
}

// mountEntry represents a single mount from the inspect Mounts array.
// nerdctl populates Mounts instead of HostConfig.Binds.
type mountEntry struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	// Target is an alias for Destination used by some nerdctl/containerd versions.
	Target string `json:"Target"`
	Mode   string `json:"Mode"`
	RW     bool   `json:"RW"`
	// Propagation holds mount propagation settings (e.g. "rprivate").
	// Docker separates this from Mode; nerdctl may put it in either field.
	Propagation string `json:"Propagation"`
}

// InspectSelf inspects the current container using os.Hostname() as the container ID.
func (c *Nerdctl) InspectSelf(_ context.Context) (runtime.ContainerInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("getting hostname: %w", err)
	}

	out, err := ctrctl.ContainerInspect(
		&ctrctl.ContainerInspectOpts{Format: "{{json .}}"},
		hostname,
	)
	if err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("inspecting container %q: %w", hostname, err)
	}

	var info runtime.ContainerInfo

	// nerdctl may return an array; try array first, then single object.
	var responses []inspectResponse
	if err := json.Unmarshal([]byte(out), &responses); err == nil && len(responses) > 0 {
		info = infoFromInspect(responses[0])
	} else {
		var resp inspectResponse
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			return runtime.ContainerInfo{}, fmt.Errorf("parsing inspect output: %w", err)
		}
		info = infoFromInspect(resp)
	}

	// nerdctl does not populate HostConfig.Privileged or HostConfig.PidMode.
	// Detect them from /proc as a fallback.
	if !info.Privileged {
		info.Privileged = detectPrivileged()
	}
	if info.PidMode == "" {
		info.PidMode = detectPidMode()
	}

	return info, nil
}

func infoFromInspect(resp inspectResponse) runtime.ContainerInfo { //nolint:gocognit // fine for now.
	// Use Config.Cmd as the command (CMD portion only, without entrypoint).
	// The container runtime applies the image's entrypoint automatically,
	// so merging entrypoint into cmd here would cause it to be doubled
	// when RunContainer creates a new container.
	cmd := resp.Config.Cmd

	// Fallback: nerdctl may not populate Config.Cmd reliably (similar to how
	// it omits HostConfig.Privileged and HostConfig.PidMode). Use the top-level
	// Args field which nerdctl always populates from the OCI process spec.
	if len(cmd) == 0 && len(resp.Args) > 0 {
		cmd = resp.Args
	}

	// Use HostConfig.Binds if available (Docker), otherwise build from Mounts (nerdctl).
	binds := resp.HostConfig.Binds
	if len(binds) == 0 && len(resp.Mounts) > 0 { //nolint:nestif // fine for now.
		for _, m := range resp.Mounts {
			if !strings.EqualFold(m.Type, "bind") {
				continue
			}
			// Resolve destination: some nerdctl/containerd versions use
			// "Target" instead of "Destination".
			dest := m.Destination
			if dest == "" {
				dest = m.Target
			}
			if dest == "" {
				continue
			}
			// Skip nerdctl-internal mounts. nerdctl creates per-container temp
			// directories (e.g. /tmp/tink-dns-XXXXX/) for /etc/resolv.conf,
			// /etc/hosts, and /etc/hostname. These sources won't exist for a
			// new container and nerdctl will create its own.
			if isNerdctlInternalMount(dest) {
				continue
			}
			bind := m.Source + ":" + dest
			opts := mountOptions(m)
			if len(opts) > 0 {
				bind += ":" + strings.Join(opts, ",")
			}
			binds = append(binds, bind)
		}
	}

	return runtime.ContainerInfo{
		Image:        resp.Config.Image,
		Env:          resp.Config.Env,
		Cmd:          cmd,
		Tty:          resp.Config.Tty,
		AttachStdout: resp.Config.AttachStdout,
		AttachStderr: resp.Config.AttachStderr,
		Privileged:   resp.HostConfig.Privileged,
		Binds:        binds,
		PidMode:      resp.HostConfig.PidMode,
	}
}

// RunContainer creates and starts a detached container with the given configuration.
func (c *Nerdctl) RunContainer(_ context.Context, info runtime.ContainerInfo) error {
	opts := &ctrctl.ContainerRunOpts{
		Detach:     true,
		Env:        info.Env,
		Volume:     info.Binds,
		Tty:        info.Tty,
		Privileged: info.Privileged,
	}

	if info.PidMode != "" {
		opts.Pid = info.PidMode
	}

	var command string
	var args []string
	if len(info.Cmd) > 0 {
		command = info.Cmd[0]
		if len(info.Cmd) > 1 {
			args = info.Cmd[1:]
		}
	}

	_, err := ctrctl.ContainerRun(opts, info.Image, command, args...)
	if err != nil {
		return fmt.Errorf("running container with image %q: %w", info.Image, err)
	}
	return nil
}

// ImageExists reports whether the given image reference exists locally.
func (c *Nerdctl) ImageExists(_ context.Context, imageRef string) bool {
	_, err := ctrctl.ImageInspect(&ctrctl.ImageInspectOpts{}, imageRef)

	return err == nil
}

// PullImage pulls the given image reference from a registry.
func (c *Nerdctl) PullImage(_ context.Context, imageRef string) error {
	_, err := ctrctl.ImagePull(
		&ctrctl.ImagePullOpts{
			Cmd: &exec.Cmd{
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			},
		},
		imageRef,
	)
	return err
}

// Close is a no-op for CLI-based runtimes.
func (c *Nerdctl) Close() error {
	return nil
}

// detectPrivileged checks if the current process has full capabilities,
// which indicates it is running in a privileged container.
// nerdctl does not populate HostConfig.Privileged in its inspect output.
func detectPrivileged() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			capHex := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
			// A privileged container has all capabilities enabled.
			// Full capability sets end with at least 9 'f' hex digits:
			//   Kernel 4.x: 0000003fffffffff
			//   Kernel 5.x+: 000001ffffffffff
			return strings.HasSuffix(capHex, "fffffffff")
		}
	}
	return false
}

// detectPidMode checks if the current process is in the host PID namespace.
// nerdctl does not populate HostConfig.PidMode in its inspect output.
// It reads NSpid from /proc/self/status: a single PID means host PID namespace,
// multiple PIDs mean a container PID namespace.
func detectPidMode() string {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			// "NSpid:" + 1 PID = host PID namespace
			// "NSpid:" + 2+ PIDs = nested/container PID namespace
			if len(fields) == 2 { //nolint:mnd // using a magic number doesn't matter.
				return "host"
			}
			return ""
		}
	}
	return ""
}

// mountOptions builds the volume option string from a mount entry's Mode,
// Propagation, and RW fields. It normalises the output to be compatible
// with the --volume flag of both Docker and nerdctl CLIs.
func mountOptions(m mountEntry) []string {
	seen := make(map[string]bool)
	var opts []string

	// Collect options from Mode (nerdctl often packs everything here,
	// e.g. "bind,rprivate,rw") and from the separate Propagation field
	// (Docker uses Propagation instead of Mode for propagation).
	collectCSV(&opts, seen, m.Mode)
	collectCSV(&opts, seen, m.Propagation)

	// Honour the RW field: if it is false the mount is read-only.
	// Add "ro" unless an explicit rw/ro option was already collected.
	if !m.RW && !seen["ro"] && !seen["rw"] {
		opts = append(opts, "ro")
	}

	return opts
}

// collectCSV splits a comma-separated option string, deduplicates entries
// (case-insensitive), and appends novel options to opts. The "bind" keyword
// is silently dropped because it is implied by the --volume flag.
func collectCSV(opts *[]string, seen map[string]bool, csv string) {
	for _, o := range strings.Split(csv, ",") {
		o = strings.TrimSpace(o)
		if o == "" || strings.EqualFold(o, "bind") {
			continue
		}
		lower := strings.ToLower(o)
		if !seen[lower] {
			seen[lower] = true
			*opts = append(*opts, o)
		}
	}
}

// isNerdctlInternalMount returns true if the destination is a nerdctl-managed
// mount that should not be propagated to new containers.
func isNerdctlInternalMount(destination string) bool {
	// nerdctlInternalDests are mount destinations that nerdctl manages internally
	// per container. These should not be propagated to child containers because
	// their source paths are temp directories unique to the parent container.
	var nerdctlInternalDests = map[string]bool{
		"/etc/resolv.conf": true,
		"/etc/hosts":       true,
		"/etc/hostname":    true,
	}
	return nerdctlInternalDests[destination]
}
