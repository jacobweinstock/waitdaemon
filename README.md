# waitdaemon

This is a container to be used as a Tinkerbell Action. It has the following purposes:

- Run any arbitrary container image with its accompanying envs, command, volumes, etc.
- Wait an arbitrary amount of time before running your specified container image.
- Immediately report back to the Tink server that the Action has completed successfully.

waitdaemon supports **Docker** and **nerdctl**. By default it auto-detects which runtime is available (preferring Docker, then probing for nerdctl). You can override this with the `CONTAINER_RUNTIME` environment variable.

waitdaemon's main use cases are kexec-ing and rebooting a machine. Currently, in Tinkerbell, these Actions generally cause the `STATE` to never transition to `SUCCESS`.
This has a few consequences.

1. If/when the machine runs Tink worker again (via a network boot, for example), this Action will be run again. The same issue with `STATE` not transitioning will continue to occur.
2. Any entity watching and expecting the `SUCCESS` state of the Action and Workflow will be unable to determine if the kexec or reboot occurred or not. [CAPT](https://github.com/tinkerbell/cluster-api-provider-tinkerbell), for example.
3. Poor user experience. A machine might have successfully kexec'd or rebooted but the `STATE` is not accurate. (This one is actually not solved by waitdaemon. A `SUCCESS` state does not guarantee the Action was successful.)  

> NOTE: waitdaemon does not guarantee your container ran successfully! Using waitdaemon means that failures in running your container are not surfaced to Tink server and your Workflow. You will need to check the Smee logs for any errors.

## Tinkerbell Action

The following Tinkerbell Action fields require specific values for waitdaemon to work properly:

| Field | Description | Required |
| --- | --- | --- |
| `image` | The image must be set to `ghcr.io/jacobweinstock/waitdaemon:latest` or a specific tag version, `ghcr.io/jacobweinstock/waitdaemon:<tag>`. | Yes |
| `pid` | This will almost always need to be set to `host` because `reboot` and `kexec` require it and nerdctl may also require it. | Yes |
| `command` | The command should be set to what you want run inside the image defined in the environment variable: `IMAGE`. | Yes |
| `environment` | The environment variables are detailed below. | Yes |
| `volumes` | The volumes settings are detailed below. | Yes |

## Environment Variables

The following are the configurable waitdaemon environment variables:

| Variable | Description | Required | Default |
| --- | --- | --- | --- |
| `IMAGE` | The container image to run after waiting. | Yes | N/A |
| `WAIT_SECONDS` | The number of seconds to wait before running the container. | No | `10` |
| `CONTAINER_RUNTIME` | The container runtime to use. Valid values are: `docker`, `nerdctl`, `auto`. | No | `auto` |
| `NERDCTL_NAMESPACE` | The namespace in which nerdctl should operate. | No | `tinkerbell` |
| `NERDCTL_HOST` | When set to `true` or `1`, nerdctl from the host will be used. | No | `true` |

## Volume Mounts

The required volume mounts depend on the container runtime you are using.

### Docker

When using the Docker (which is the only runtime available in [HookOS](https://github.com/tinkerbell/hook)), the Docker socket must be mounted:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

### nerdctl

When using nerdctl (which is the only runtime available in [CaptainOS](https://github.com/tinkerbell/captain)), volume mounts are only required if `NERDCTL_HOST` is `false` or when set to `true` and it does not work for your setup.

```yaml
volumes:
    - /run/containerd/containerd.sock:/run/containerd/containerd.sock
    - /var/lib/containerd:/var/lib/containerd
    - /var/lib/nerdctl:/var/lib/nerdctl
    - /opt/cni/bin:/opt/cni/bin
    - /etc/cni/net.d:/etc/cni/net.d
```

## Tinkerbell Operating System Installation Environments (OSIE)

waitdaemon is compatible with both [HookOS](https://github.com/tinkerbell/hook) and [CaptainOS](https://github.com/tinkerbell/captain).

## Usage

Here are two example Actions that will work with both HookOS and CaptainOS:

- Reboot a machine:

  ```yaml
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    command: ["reboot"]
    environment:
      IMAGE: alpine
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  ```

- kexec into a new kernel:

  ```yaml
  - name: "kexec"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    environment:
      BLOCK_DEVICE: {{ formatPartition ( index .Hardware.Disks 0 ) 1 }}
      FS_TYPE: ext4
      IMAGE: quay.io/tinkerbell-actions/kexec:v1.0.0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  ```

## Advanced Usage

- When `NERDCTL_HOST` is `false`:

  ```yaml
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    command: ["reboot"]
    environment:
      IMAGE: alpine
    volumes:
      - /run/containerd/containerd.sock:/run/containerd/containerd.sock
      - /var/lib/containerd:/var/lib/containerd
      - /var/lib/nerdctl:/var/lib/nerdctl
      - /opt/cni/bin:/opt/cni/bin
      - /etc/cni/net.d:/etc/cni/net.d
  ```

- When you want to explicitly use Docker:

  ```yaml
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    command: ["reboot"]
    environment:
      IMAGE: alpine
      CONTAINER_RUNTIME: docker
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  ```

- When you want to explicitly use nerdctl:

  ```yaml
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    command: ["reboot"]
    environment:
      IMAGE: alpine
      CONTAINER_RUNTIME: nerdctl
  ```

- When you want to customize the wait time:

  ```yaml
  - name: "reboot"
    image: ghcr.io/jacobweinstock/waitdaemon:latest
    timeout: 90
    pid: host
    command: ["reboot"]
    environment:
      IMAGE: alpine
      WAIT_SECONDS: 30
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  ```

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
