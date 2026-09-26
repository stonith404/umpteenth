---
title: gVisor
description: Run every sandbox under gVisor, so code that escapes its container meets a second kernel instead of the host's.
---

[gVisor](https://gvisor.dev) puts a kernel of its own, running in user space, between each sandbox and the host.
Code that breaks out of a sandbox's container lands in gVisor's kernel, which passes only a short, filtered list of system calls on to the host.
To use it, you install its `runsc` runtime in Docker and point Umpteenth at it.

gVisor guards the host against the sandboxes and leaves Umpteenth's own access to the Docker socket as it is, see [Security](../security/#the-docker-socket).

## Install runsc

You need Docker on a Linux host with an x86-64 or arm64 CPU.
Download the latest release and verify its checksum:

```bash
url="https://storage.googleapis.com/gvisor/releases/release/latest/$(uname -m)"
wget "$url/gvisor.tar.bz2" "$url/gvisor.tar.bz2.sha512"
sha512sum -c gvisor.tar.bz2.sha512
```

Unpack it and copy `runsc`, its containerd shim and the `gvisor-bin/` helpers to `/usr/local/bin/`, since `runsc` looks for the helpers in its own directory:

```bash
mkdir gvisor && tar -xjf gvisor.tar.bz2 -C gvisor
sudo cp -r gvisor/runsc gvisor/containerd-shim-runsc-v1 gvisor/gvisor-bin /usr/local/bin/
```

Register the runtime with Docker, with two flags, and restart the engine:

```bash
sudo /usr/local/bin/runsc install -- --overlay2=none --file-access=shared
```

```bash
sudo systemctl restart docker
```

Umpteenth copies files into running sandboxes and reads the files a run writes through the Docker API, and the two flags make those files visible on both sides.
The install command adds this entry to `/etc/docker/daemon.json`:

```json title="/etc/docker/daemon.json"
{
  "runtimes": {
    "runsc": {
      "path": "/usr/local/bin/runsc",
      "runtimeArgs": ["--overlay2=none", "--file-access=shared"]
    }
  }
}
```

:::caution[Keep runc as Docker's default runtime]
Register `runsc` as an extra runtime and point Umpteenth at it by name.
If you make `runsc` the engine's `default-runtime` instead and leave Umpteenth's runtime at `runc`, Umpteenth treats the sandboxes as ordinary containers and skips the DNS setup they need under gVisor.
:::

## Turn it on

Set the sandbox runtime to `runsc` in `config.yml`, or set `SANDBOX_DOCKER_RUNTIME=runsc` in the [environment](../configuration/#environment-variables):

```yaml title="config.yml"
sandbox:
  docker:
    runtime: runsc
```

Restart Umpteenth after an edit to `config.yml`:

```bash
docker compose restart umpteenth
```

A change to `environment:` needs `docker compose up -d` instead.

Open **Settings → General**.
The **Sandbox backend** card shows `gvisor` under **Isolation**.
Every run from now on shows `gvisor` in the **Sandbox** stat of its run page and on its **Sandbox created** step.

## If sandboxes don't start

Each time the server starts, Umpteenth runs a test sandbox under `runsc` and moves a file into it and back out.
If the test fails, the **Sandbox backend** card shows **Sandboxes can't start under the configured runtime** with the reason, and runs fail with `Failed to create the sandbox:` followed by the same reason.

- `files copied into a running sandbox are invisible to its commands` or `files written in a sandbox can't be read from outside`: the two flags are missing from `daemon.json`.
  Run the `runsc install` command above and restart Docker.
- `a test sandbox under runtime runsc failed to start`: Docker couldn't start the container at all, and the rest of the message is Docker's error.
  `runsc` fails this way when its `gvisor-bin/` helpers aren't next to it.
- The card says **No sandbox backend is configured.**: Docker doesn't know a runtime named `runsc`.
  Check `docker info --format '{{json .Runtimes}}'` and the `daemon.json` entry above.

Umpteenth tests the runtime once per start, so restart it after you fix the engine:

```bash
docker compose restart umpteenth
```

## Differences under gVisor

Sandboxes take a moment longer to start, and file-heavy work such as installing packages runs slower, since every file access passes through gVisor.
Docker builds [job Dockerfiles](../../guides/sandboxes/) with its default runtime, so build steps run outside gVisor.

### DNS

gVisor's network stack can't reach Docker's embedded DNS server.
Umpteenth gives sandboxes an `/etc/hosts` entry for its broker instead, and points sandboxes with **Internet access** at the public resolvers `8.8.8.8` and `8.8.4.4`.
Set `sandbox.docker.dns` to use other resolvers, such as `[1.1.1.1, 9.9.9.9]`.
The [egress firewall](../security/#the-egress-firewall) blocks private addresses, so a resolver on your LAN works only for jobs with **Allow private network**.
Jobs with **Allowed domains only** or **No network** don't resolve names themselves and ignore the setting.

### Memory

gVisor's kernel counts toward the sandbox's memory limit, so a command that runs out of memory takes the whole sandbox down with it.
Umpteenth then answers the agent's command with `The sandbox is gone (it may have run out of memory). The run cannot continue.`
Raise the job's **Memory** in its **Settings** tab, see [Managing jobs](../../guides/jobs/).
