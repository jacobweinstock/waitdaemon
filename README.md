# waitdaemon

This is a container to be used as a Tinkerbell action. It has the following purposes:

- Run any arbitrary container image with its accompanying envs, command, volumes, etc.
- Wait an arbitrary amount of time before running your specified container image.
- Immediately report back to the Tink server that the action has completed successfully.

waitdaemon supports **Docker** (via the Docker SDK) and **nerdctl** (via the [ctrctl](https://github.com/lesiw/ctrctl) CLI wrapper library). By default it auto-detects which runtime is available (preferring the Docker SDK when the Docker socket is present, then probing for docker and nerdctl CLIs). You can override this with the `CONTAINER_RUNTIME` environment variable. A static `nerdctl` binary is bundled in the container image.

waitdaemon's main use cases are kexec-ing and rebooting a machine. Currently, in Tinkerbell, these action generally cause the `STATE` to never transition to `STATE_SUCCESS`.
This has a few consequences.

1. If/when the machine runs Tink worker again (via a network boot, for example), this action to be run again. The same issue with `STATE` not transistioning will continue to occur.
2. Any entity watching and expecting the `STATE_SUCCESS` of the action and of the whole workflow will be unable to determine if the kexec or reboot occured or not. [CAPT](https://github.com/tinkerbell/cluster-api-provider-tinkerbell), for example.
3. Poor user experience. A machine might have successfully kexec'd or rebooted but the `STATE` is not accurate. (This one is actually not solved by waitdaemon. A `STATE_SUCCESS` does not guarantee the action was successful.)  

> NOTE: waitdaemon does not guarantee the action was successful! Using this image means that failures in running your container are not surfaced to Tink server and your workflow. You will need to check the Smee logs for details.

## Usage

Here are two example actions:

```yaml
- name: "reboot"
  image: ghcr.io/jacobweinstock/waitdaemon:latest
  timeout: 90
  pid: host
  command: ["reboot"]
  environment:
    IMAGE: alpine
    WAIT_SECONDS: 10
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
```

```yaml
- name: "kexec"
  image: ghcr.io/jacobweinstock/waitdaemon:latest
  timeout: 90
  pid: host
  environment:
    BLOCK_DEVICE: {{ formatPartition ( index .Hardware.Disks 0 ) 1 }}
    FS_TYPE: ext4
    IMAGE: quay.io/tinkerbell-actions/kexec:v1.0.0
    WAIT_SECONDS: 10
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
```

### With nerdctl (containerd)

```yaml
- name: "reboot"
  image: ghcr.io/jacobweinstock/waitdaemon:latest
  timeout: 90
  pid: host
  privileged: true
  command: ["reboot"]
  environment:
    IMAGE: alpine
    WAIT_SECONDS: 10
    CONTAINER_RUNTIME: containerd
  volumes:
    - /run/containerd/containerd.sock:/run/containerd/containerd.sock
    - /var/lib/containerd:/var/lib/containerd
    - /var/lib/nerdctl:/var/lib/nerdctl
    - /opt/cni/bin:/opt/cni/bin
    - /etc/cni/net.d:/etc/cni/net.d
```

### With nerdctl + nsenter (no volume mounts)

When `NSENTER_HOST` is enabled, all nerdctl CLI calls are prefixed with `nsenter -t 1 -m -u -i -n -p --` so they execute inside all host namespaces. This means nerdctl can access containerd's socket and state directories directly through the host filesystem, **eliminating the need for volume mounts** in the Tinkerbell template. The host must have `nerdctl` installed and available on its `PATH`. The container must still use `pid: host` so that nsenter can reach host PID 1.

> NOTE: `NSENTER_HOST` only applies to the nerdctl/containerd runtime path — it has no effect when using the Docker SDK.

```yaml
- name: "reboot"
  image: ghcr.io/jacobweinstock/waitdaemon:latest
  timeout: 90
  pid: host
  privileged: true
  command: ["reboot"]
  environment:
    IMAGE: alpine
    WAIT_SECONDS: 10
    CONTAINER_RUNTIME: containerd
    NSENTER_HOST: "true"
```

### Required fields

- ```yaml
  image: ghcr.io/jacobweinstock/waitdaemon:latest
  ```

- This value will tell us the image to run after waiting the duration of `WAIT_SECONDS`.

  ```yaml
  environment:
    IMAGE: <your image>
  ```

- When using `CONTAINER_RUNTIME=docker` (or auto-detect with Docker), the Docker socket mount is needed:

  ```yaml
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
  ```

- When using `CONTAINER_RUNTIME=containerd` (or auto-detect with nerdctl), the following volume mounts are required:

  ```yaml
  volumes:
    - /run/containerd/containerd.sock:/run/containerd/containerd.sock
    - /var/lib/containerd:/var/lib/containerd
    - /var/lib/nerdctl:/var/lib/nerdctl
    - /opt/cni/bin:/opt/cni/bin
    - /etc/cni/net.d:/etc/cni/net.d
  ```

  The container must also run as `privileged: true` so that nerdctl can perform overlay mounts for container filesystems.

### Optional Settings

- `WAIT_SECONDS`: This is the number of seconds to wait before running your container.
- `CONTAINER_RUNTIME`: The container runtime to use. Valid values are:
  - `docker` — Use the Docker SDK (requires Docker socket mount).
  - `containerd` — Backward-compatible alias: uses the bundled `nerdctl` CLI via the ctrctl wrapper.
  - `auto` (default) — Prefers Docker SDK when the Docker socket is available, then falls back to CLI auto-detection (docker > nerdctl).
- `CONTAINERD_NAMESPACE`: The containerd namespace nerdctl should operate in (default `tinkerbell`). Only applies when the resolved CLI is nerdctl (i.e. `CONTAINER_RUNTIME` is `containerd`, or auto selects nerdctl).
- `NSENTER_HOST`: When set to `true` or `1`, all nerdctl CLI calls are prefixed with `nsenter -t 1 -m -u -i -n -p --` so they execute inside all host namespaces. This eliminates the need for volume mounts when using nerdctl/containerd. The host must have `nerdctl` installed. The container must still use `pid: host`. This setting only applies to the nerdctl/containerd runtime path — it has no effect when using the Docker SDK.

### Details

Under the hood, the waitdaemon is doing something akin to daemonizing or double forking a Linux process but for containers and a Tinkerbell action.
All values you specify in your action. `command`, `volumes`, `pid`, `environment`, etc are propogated to your container image when it's run.

```txt
┌──────────────Action Container───────────────┐
│                                             │
│   image: ghcr.io/jacobweinstock/waitdaemon  │
│                                             │
│             ┌────process─────┐              │
│             │   waitdaemon   │              │
│             └────────┬───────┘              │
└──────────────────────┼──────────────────────┘
                       │                       
                 create, exit                  
                       │                       
                       ▼                       
┌──────────────────Container──────────────────┐
│                                             │
│   image: ghcr.io/jacobweinstock/waitdaemon  │
│                                             │
│             ┌────process─────┐              │
│             │   waitdaemon   │              │
│             └────────┬───────┘              │
└──────────────────────┼──────────────────────┘
                       │                       
               wait, create, exit              
                       │                       
                       ▼                       
┌──────────────────Container──────────────────┐
│                                             │
│              image: your image              │
│                                             │
│                                             │
│                                             │
│                                             │
└─────────────────────────────────────────────┘
```
