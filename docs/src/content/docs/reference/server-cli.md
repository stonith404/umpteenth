---
title: Server CLI
description: The commands of the umpteenth binary, from serve and healthcheck to export and import, and how to run them with Docker.
---

The Umpteenth image runs the binary `/app/umpteenth` as its entrypoint.
Without a subcommand it starts the server, and you pass a subcommand for anything else:

| Command | Effect |
|---|---|
| `umpteenth`, `umpteenth serve` | Starts the server |
| `umpteenth healthcheck` | Exits with `0` when the local server is healthy |
| `umpteenth openapi` | Prints the OpenAPI spec of the REST API as JSON |
| `umpteenth version` | Prints the version |
| `umpteenth export` | Exports jobs, playbooks, MCP servers, providers and settings as JSON |
| `umpteenth import <file>` | Imports an export into an instance |

`umpteenth help` lists the commands, and `--help` after a command lists its flags.

## Running a command

The image has no shell and `/app` isn't on its `PATH`, so call the binary by its full path inside the running container:

```bash
docker exec umpteenth /app/umpteenth version
```

On another machine that has the app image, `docker run` starts a throwaway container and passes the arguments to the binary:

```bash
docker run --rm ghcr.io/stonith404/umpteenth:latest version
```

This suits `openapi`, `version`, `export` and `import`, which need no server in the same container.
Umpteenth doesn't publish its images yet, so build the app image on that machine first, as [Installation](../../getting-started/installation/) shows.

## serve

`serve` loads the configuration and applies pending database migrations.
Then it starts the UI and API on `server.port` (8080) and the sandbox broker on `server.broker_port` (8081).
The broker is the API that [`ump`](../ump-cli/) calls from inside sandboxes.
`serve` shuts down on `SIGTERM`, which `docker stop` sends, or on `SIGINT`.

`serve`, `healthcheck` and the bare `umpteenth` take one flag:

| Flag | Default |
|---|---|
| `--config <file>` | `CONFIG_FILE`, else `config.yml` or `config.yaml` in the working directory if one exists |

Environment variables override the file's options.
The image's working directory is `/app`, so the Compose file's mount at `/app/config.yml` needs no flag.
[Configuration](../../deployment/configuration/) lists the options.

## healthcheck

`healthcheck` reads the same configuration as `serve` to find the port, then requests `http://127.0.0.1:<port>/healthz` with a 5-second timeout.
It exits with `0` when the server answers `204`.
Otherwise it prints the error and exits with `1`, for example `unhealthy: status 503` when the server can't reach its database.

The image's `HEALTHCHECK` runs it every 30 seconds with a 5-second timeout and a 20-second start period, and `docker ps` shows the result as `healthy` or `unhealthy`.
The command connects to `127.0.0.1`, so if you bind `server.host` to one specific address other than `127.0.0.1`, the check can't connect and Docker marks the container `unhealthy`.
[Upgrades and maintenance](../../deployment/upgrading/) covers `/healthz` for external monitors.

## openapi

`openapi` prints the OpenAPI spec as indented JSON, the same document the server serves at `/api/openapi.json`.
It needs neither a configuration nor a running server:

```bash
docker run --rm ghcr.io/stonith404/umpteenth:latest openapi > openapi.json
```

[REST API](../api/) describes the routes and how to authenticate.

## version

`version` prints the binary's version.
The `VERSION` build argument sets it and defaults to `dev`, so an image you build with the command from [Installation](../../getting-started/installation/) prints `dev`.
`GET /api/system/info` reports the same value.

## export and import

Both commands are API clients: they call an instance over HTTP with an API token and read none of the server's configuration, so they work against any instance you can reach.
[Backups and migration](../../deployment/backups/) explains what an export contains and how an import matches what the instance already has.

They share two flags:

| Flag | Default | Notes |
|---|---|---|
| `--url <url>` | `UMPTEENTH_URL`, else `APP_URL`, else `http://localhost:8080` | The instance to call |
| `--token <token>` | `UMPTEENTH_API_TOKEN` | An API token from **Settings → API tokens**, required |

Without a token, both stop with "an API token is required: pass --token or set UMPTEENTH_API_TOKEN".
They read `APP_URL` from the environment only, never from `config.yml`.
Inside the container, pass `--url http://localhost:8080` to keep the call local whatever the environment holds.
Each request times out after one minute, and an error from the API prints as `API answered <status>: <message>`.

### export

`export` takes no arguments and prints the export to stdout.

| Flag | Effect |
|---|---|
| `--history` | Includes each job's earlier playbook versions along with the current one |
| `-o`, `--output <file>` | Writes the export to a file with mode `0600` and prints `Exported N jobs, N MCP servers and N providers to <file>` to stderr |

Inside the container, point `--url` at the local server and redirect stdout.
Your shell on the host handles the redirect, so the file lands on the host:

```bash
docker exec -e UMPTEENTH_API_TOKEN=ump_… umpteenth /app/umpteenth export --url http://localhost:8080 --history > umpteenth-export.json
```

With `-o` in place of the redirect, the file lands inside the container.
From another machine, run the image against the instance's public URL:

```bash
docker run --rm -e UMPTEENTH_API_TOKEN=ump_… ghcr.io/stonith404/umpteenth:latest export --url https://umpteenth.example.com > umpteenth-export.json
```

### import

`import` takes the path of one export file.

| Flag | Effect |
|---|---|
| `--secrets <file>` | A dotenv file of `KEY=value` lines with values for the secrets the export names |
| `--dry-run` | Prints the report and changes nothing |

`import` opens its argument as a plain path, so you can pass `/dev/stdin` and pipe an export in from the host with `docker exec -i`:

```bash
docker exec -i -e UMPTEENTH_API_TOKEN=ump_… umpteenth /app/umpteenth import --url http://localhost:8080 /dev/stdin < umpteenth-export.json
```

With `docker exec`, `--secrets` names a file inside the container, since the binary runs there.
Put the file in `./data` on the host, which the Compose file mounts at `/app/data`, and pass `--secrets /app/data/secrets.env`.
Delete it after the import.

After the import, `import` prints a report: `Created:` (`Would create:` with `--dry-run`) and `Skipped:` list what it did, and `Left to do:` lists the steps you finish by hand.
`import` prints `Nothing to import` when the report has no lines at all.
If the import stops on an error, it prints the report up to that point, then the error.

## Environment variables

| Variable | Read by | Effect |
|---|---|---|
| `CONFIG_FILE` | `serve`, `healthcheck` | The config file when you pass no `--config` |
| `UMPTEENTH_URL` | `export`, `import` | The default of `--url` |
| `APP_URL` | `export`, `import`, `serve` | The default of `--url` when `UMPTEENTH_URL` is empty, and `app.url` for `serve` |
| `UMPTEENTH_API_TOKEN` | `export`, `import` | The default of `--token` |

Server options have environment variables of their own, such as `SERVER_PORT` for `server.port`, which [Configuration](../../deployment/configuration/) lists.
