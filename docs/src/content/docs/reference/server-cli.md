---
title: Server CLI
description: The commands of the umpteenth server binary, their flags, and how to run them with Docker.
---

The Umpteenth image runs the binary `/app/umpteenth` as its entrypoint.
Without a subcommand it starts the server, and you pass a subcommand for anything else:

| Command | Effect |
|---|---|
| `umpteenth`, `umpteenth serve` | Applies pending database migrations, then serves the UI and API on `server.port` (8080) and the sandbox broker on `server.broker_port` (8081) |
| `umpteenth healthcheck` | Exits with `0` if the local server's `/healthz` reports it healthy, and with `1` otherwise |
| `umpteenth openapi` | Prints the OpenAPI spec of the [REST API](../api/) as JSON, with no configuration or running server needed |
| `umpteenth version` | Prints the version, the same value `GET /api/system/info` reports |

`umpteenth help` lists the commands, and `--help` after a command lists its flags.

## Running a command

The image has no shell and `/app` isn't on its `PATH`, so call the binary by its full path inside the running container:

```bash
docker exec umpteenth /app/umpteenth version
```

A release image prints its version, such as `1.2.3`, and a `next` image prints `next-` followed by the short commit hash.

On any machine with Docker, `docker run` starts a throwaway container from the published image and passes the arguments to the binary.
This suits `openapi` and `version`, which need no server in the same container:

```bash
docker run --rm ghcr.io/stonith404/umpteenth:latest openapi > openapi.json
```

## serve and healthcheck

`serve`, `healthcheck` and the bare `umpteenth` read the server's configuration and take one flag:

| Flag | Default |
|---|---|
| `--config <file>` | `CONFIG_FILE`, else `config.yml` or `config.yaml` in the working directory if one exists |

Environment variables override the file's options, and [Configuration](../../deployment/configuration/) lists them all.

The image's health check runs `healthcheck`, so `docker ps` shows the container as `healthy` or `unhealthy`.
The command connects to `127.0.0.1` on `server.port`, so it fails if you bind `server.host` to a single address other than `127.0.0.1`.
[Upgrades and maintenance](../../deployment/upgrading/) covers `/healthz` for external monitors.
