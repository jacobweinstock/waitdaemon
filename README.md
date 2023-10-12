# waitdaemon
This is a container to be used as a Tinkerbell action. It has the following purposes:

- Run any arbitrary container image with its accompanying envs, command, volumes, etc.
- Wait an arbitrary amount of time before running your specified container image.
- Immediately report back to the Tink server that the action has completed successfully.

waitdaemon's main use cases are kexec-ing and rebooting a machine. Currently, in Tinkerbell, these action generally cause the `STATE` to never transition to `STATE_SUCCESS`.
This has a few consequences.
1. If/when the machine runs Tink worker again (via a network boot, for example), this action to be run again. The same issue with `STATE` not transistioning will continue to occur.
2. Any entity watching and expecting the `STATE_SUCCESS` of the action and of the whole workflow will be unable to determine if the kexec or reboot occured or not. [CAPT](https://github.com/tinkerbell/cluster-api-provider-tinkerbell), for example.
3. Poor user experience. A machine might have successfully kexec'd or rebooted but the `STATE` is not accurate.

## Usage

Here are two example actions:

```yaml
- name: "reboot"
  image: ghcr.io/jacobweinstock/waitdaemon
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
  image: ghcr.io/jacobweinstock/waitdaemon
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

### Required fields

- ```yaml
  image: ghcr.io/jacobweinstock/waitdaemon
  ```
- This value will tell us the image to run after waiting the duration of `WAIT_SECONDS`.
  ```yaml
  environment:
    IMAGE: <your image>
  ```  
- This is needed so we can create Docker containers that the Tink worker doesn't wait on.
  ```yaml
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
  ```

### Optional Settings

- `WAIT_SECONDS`: This is the number of seconds to wait before running your container.

### Details

Under the hood, the waitdaemon is doing something akin to daemonizing or double forking a Linux process but for containers and a Tinkerbell action.
All values you specify in your action. `command`, `volumes`, `pid`, `environment`, etc are propogated to your container image when it's run.

```
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
