---
title: Configuration
description: Set Umpteenth's options in config.yml or through environment variables, with every option and its default.
---

You configure Umpteenth in a YAML file, `config.yml`, and override single options with environment variables.

## The config file

`docker-compose.yml` mounts `config.yml` from its own directory into the container at `/app/config.yml`, where Umpteenth looks for it:

```yaml title="docker-compose.yml"
services:
  umpteenth:
    volumes:
      - ./config.yml:/app/config.yml:ro
```

Start from `config.example.yml` in the repository, which lists every option with its default and a comment.
Options you leave out or leave empty keep their defaults, so a short file is enough:

```yaml title="config.yml"
app:
  url: https://umpteenth.example.com
  encryption_key: "<output of openssl rand -base64 32>"

oidc:
  issuer: https://id.example.com
  client_id: umpteenth
  client_secret: "<client secret>"
  allowed_groups: [umpteenth-admins]
```

Umpteenth reads the file once, when it starts, so restart it after an edit:

```bash
docker compose restart umpteenth
```

Without a `config.yml`, Umpteenth falls back to `config.yaml`.
To keep the file somewhere else in the container, point `CONFIG_FILE` or the `--config` flag at it, and Umpteenth refuses to start if that file is missing.

:::caution[Create the file before the first start]
If `config.yml` doesn't exist when the container starts, Docker creates an empty directory named `config.yml` in its place.
Umpteenth then has no config file, and without a key in the environment it exits with `app.encryption_key (APP_ENCRYPTION_KEY) is required`.
Remove the directory, copy the example to `config.yml` and start again.
:::

## Environment variables

Each option has an environment variable named after its path, in upper case with underscores, so `server.trust_proxy` is `SERVER_TRUST_PROXY`.
Umpteenth applies its defaults first, then `config.yml`, then the environment, so a variable wins over the file.
Set variables in the `environment:` block of the service:

```yaml title="docker-compose.yml"
services:
  umpteenth:
    environment:
      LOG_LEVEL: debug
      OIDC_ALLOWED_GROUPS: admins,ops
```

Apply a change to `environment:` with `docker compose up -d`, which recreates the container.
`docker compose restart` keeps the old environment.

An empty variable counts as unset and keeps the value from the file.
Lists take commas, as `OIDC_ALLOWED_GROUPS` shows, and durations take a unit, such as `90m` or `12h`.

Docker Compose reads a `.env` file next to `docker-compose.yml` to fill `${...}` placeholders in the compose file, and passes nothing from it to Umpteenth on its own.
To use a variable from `.env`, reference it in `environment:`, such as `LOG_LEVEL: ${LOG_LEVEL}`, or add `env_file: .env` to the service.

### Values from files

Append `_FILE` to any variable to read its value from a file, the way Docker secrets work:

```yaml title="docker-compose.yml"
services:
  umpteenth:
    environment:
      APP_ENCRYPTION_KEY_FILE: /run/secrets/encryption_key
    secrets:
      - encryption_key

secrets:
  encryption_key:
    file: ./encryption_key.txt
```

Umpteenth trims whitespace around the content, so a trailing newline does no harm.
Set a variable or its `_FILE` variant, not both, or Umpteenth refuses to start with `set either APP_ENCRYPTION_KEY or APP_ENCRYPTION_KEY_FILE, not both`.

## Errors at start

Umpteenth checks the configuration before it opens the database, and a typo in the file or an invalid value stops it with an error that names the option.
Read the error with `docker compose logs umpteenth`:

```txt
Error: config.yml: line 4: unknown option server.prot
Error: unknown file_storage.backend (FILE_STORAGE_BACKEND) "nfs", use filesystem, s3 or database
```

## Workspace defaults

Three options set defaults for fields you can also change under **Settings → General**:

| Option | Field |
|---|---|
| `sandbox.image` | **Default image** on the **Sandbox defaults** card |
| `runs.daily_spend_limit_usd` | **Daily spend limit** on the **Spend and retention** card |
| `runs.retention_days` | **Retention** on the **Spend and retention** card |

Your workspace uses the option's current value until someone saves the card that holds the field.
Saving a card stores every field on it, so a change to **Memory** on the **Sandbox defaults** card stores **Default image** too.
From then on, edits to `sandbox.image` leave the workspace's default image alone.

## Options

Each row shows the option's path in `config.yml` above its environment variable.

### App

| Option | Default | Description |
|---|---|---|
| `app.url`<br />`APP_URL` | `http://localhost:8080` | The URL you open Umpteenth at. The OIDC redirect URI is `<app.url>/api/auth/callback`, and links in notifications point to runs under it. With an `https://` URL, Umpteenth marks its cookies `Secure`, see [Reverse proxy](../reverse-proxy/). |
| `app.encryption_key`<br />`APP_ENCRYPTION_KEY` | none, required | At least 16 bytes, such as the output of `openssl rand -base64 32`. Encrypts secrets, provider API keys and MCP logins, and signs sessions. Under a different key, Umpteenth can't decrypt what it stored and everyone has to sign in again, so keep a copy with your [backups](../backups/). |
| `app.data_dir`<br />`APP_DATA_DIR` | `data`, which is `/app/data` in the container | Holds the SQLite database and stored files, unless `database.connection_string` or `file_storage.path` point elsewhere. |

### Sign-in

| Option | Default | Description |
|---|---|---|
| `oidc.issuer`<br />`OIDC_ISSUER` | none | The issuer URL of your identity provider. Umpteenth fetches its discovery document from inside the container at the first sign-in, so the URL has to work there, where `localhost` is the container itself. |
| `oidc.client_id`<br />`OIDC_CLIENT_ID` | none | The client ID. Without an issuer and a client ID, the login page shows "Sign-in is not configured on this server". |
| `oidc.client_secret`<br />`OIDC_CLIENT_SECRET` | empty | The client secret. Umpteenth signs in with PKCE, so a public client without a secret works too. |
| `oidc.allowed_groups`<br />`OIDC_ALLOWED_GROUPS` | empty | Groups whose members may sign in. Umpteenth compares them, case included, with the ID token's `groups` claim. Empty admits everyone your identity provider lets through, see [Security](../security/#sign-in-and-sessions). |

### Server

| Option | Default | Description |
|---|---|---|
| `server.host`<br />`SERVER_HOST` | `0.0.0.0` | The interface both listeners bind to. Sandboxes reach the broker over Docker networks, so keep the default in a container. |
| `server.port`<br />`SERVER_PORT` | `8080` | The UI, the API and job webhooks. |
| `server.broker_port`<br />`SERVER_BROKER_PORT` | `8081` | The broker, the API that the `ump` CLI inside sandboxes calls for models, MCP tools and job state. Keep it unpublished. |
| `server.trust_proxy`<br />`SERVER_TRUST_PROXY` | `false` | Takes the client address for the login rate limit from `X-Forwarded-For`. Turn it on only behind a [reverse proxy](../reverse-proxy/#trust-the-proxy). |

### Logs

| Option | Default | Description |
|---|---|---|
| `log.level`<br />`LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, and any other value stops the server. At `debug` Umpteenth logs every API request, at `info` the failed ones. |
| `log.json`<br />`LOG_JSON` | `false` | JSON lines instead of text. |

### Database

| Option | Default | Description |
|---|---|---|
| `database.connection_string`<br />`DATABASE_CONNECTION_STRING` | `<app.data_dir>/umpteenth.db` | A SQLite file path, or a Postgres URL starting with `postgres://` or `postgresql://`, such as `postgres://umpteenth:secret@db:5432/umpteenth?sslmode=disable`. Umpteenth treats anything else as a SQLite path, a `host=db user=umpteenth` connection string included. |

### File storage

| Option | Default | Description |
|---|---|---|
| `file_storage.backend`<br />`FILE_STORAGE_BACKEND` | `filesystem` | The store for artifacts, long tool outputs and image build logs: `filesystem`, `s3`, or `database` for a table in the database. |
| `file_storage.path`<br />`FILE_STORAGE_PATH` | `<app.data_dir>/blobs` | The directory of the `filesystem` backend. |
| `file_storage.s3.endpoint`<br />`FILE_STORAGE_S3_ENDPOINT` | empty, meaning AWS S3 | A host with a port, such as `s3.example.com:9000`, or a URL, such as `http://s3:8333`. |
| `file_storage.s3.bucket`<br />`FILE_STORAGE_S3_BUCKET` | empty | The bucket, which Umpteenth creates at start if it's missing. |
| `file_storage.s3.region`<br />`FILE_STORAGE_S3_REGION` | empty | The region of the bucket. |
| `file_storage.s3.access_key_id`<br />`FILE_STORAGE_S3_ACCESS_KEY_ID` | empty | The access key. |
| `file_storage.s3.secret_access_key`<br />`FILE_STORAGE_S3_SECRET_ACCESS_KEY` | empty | The secret key. |
| `file_storage.s3.use_ssl`<br />`FILE_STORAGE_S3_USE_SSL` | `true` | TLS for an endpoint given as a host. An endpoint given as a URL takes TLS from its scheme. |

### Sandboxes

| Option | Default | Description |
|---|---|---|
| `sandbox.image`<br />`SANDBOX_IMAGE` | `ghcr.io/stonith404/umpteenth-sandbox:latest` | The workspace default of **Default image**. Umpteenth pulls it at start if the engine lacks it, and runs its own helper containers from it. |
| `sandbox.egress_filter`<br />`SANDBOX_EGRESS_FILTER` | `auto` | `auto`, `required` or `off`, for the firewall rules that keep internet sandboxes off private networks, see [Security](../security/#the-egress-firewall). |
| `sandbox.docker.runtime`<br />`SANDBOX_DOCKER_RUNTIME` | `runc` | The runtime of sandbox containers, `runsc` for [gVisor](../gvisor/). |
| `sandbox.docker.dns`<br />`SANDBOX_DOCKER_DNS` | empty, `8.8.8.8` and `8.8.4.4` under gVisor | The resolvers of **Internet access** sandboxes under gVisor. Set it only together with `runsc`. |
| `sandbox.registry.repository`<br />`SANDBOX_REGISTRY_REPOSITORY` | empty | A registry path without a scheme, such as `ghcr.io/acme/umpteenth-jobs`. Umpteenth pushes the image of each job with its own Dockerfile below it, as `<path>/job-<job id>`, so other replicas pull it instead of building it again. Umpteenth never deletes tags there, so give the registry a cleanup policy. |
| `sandbox.registry.username`<br />`SANDBOX_REGISTRY_USERNAME` | empty | The user for pushes and pulls. |
| `sandbox.registry.password`<br />`SANDBOX_REGISTRY_PASSWORD` | empty | The password or token of that user. |

Umpteenth finds Docker or Podman through the standard Docker client variables `DOCKER_HOST`, `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`, which have no key in `config.yml`.
Without them it uses the socket at `/var/run/docker.sock`.
If Umpteenth can't reach the engine, it refuses to start and logs `failed to reach the container engine`.

### Runs

| Option | Default | Description |
|---|---|---|
| `runs.max_concurrent`<br />`RUNS_MAX_CONCURRENT` | `3` | Runs executing at once on this replica. More runs wait as `queued`. |
| `runs.daily_spend_limit_usd`<br />`RUNS_DAILY_SPEND_LIMIT_USD` | `0`, meaning no limit | The workspace default of **Daily spend limit**, see [Models and costs](../../guides/models/#daily-spend-limit). |
| `runs.retention_days`<br />`RUNS_RETENTION_DAYS` | `90` | The workspace default of **Retention**. Every night Umpteenth deletes the events and files of runs that finished longer ago than that, and keeps the run records. |

### Network

| Option | Default | Description |
|---|---|---|
| `network.allow_private_targets`<br />`NETWORK_ALLOW_PRIVATE_TARGETS` | `true` | Lets Umpteenth itself call model providers, HTTP MCP servers and notification webhooks at private or local addresses. Sandboxes follow their job's network setting instead, see [Security](../security/#umpteenths-own-outbound-calls). |

### Models

| Option | Default | Description |
|---|---|---|
| `models.catalog_refresh_interval`<br />`MODELS_CATALOG_REFRESH_INTERVAL` | `12h` | The interval at which Umpteenth downloads the [models.dev](https://models.dev) catalog and syncs every provider's model list, see [Models and costs](../../guides/models/#keeping-the-list-current). `0` turns the refresh off, so Umpteenth uses the newer of the catalog it downloaded last and the one built into it. Any other value must be at least `5m`. |
| `providers.anthropic_api_key`<br />`PROVIDERS_ANTHROPIC_API_KEY` | empty | The API key of the **Anthropic** provider Umpteenth creates on its first start. Later starts ignore it, so change keys under **Settings → Providers & models**. |

### High availability

| Option | Default | Description |
|---|---|---|
| `ha.enabled`<br />`HA_ENABLED` | `false` | Lets several replicas share one Postgres database, and needs a `postgres://` connection string. |
| `ha.replica_id`<br />`HA_REPLICA_ID` | the hostname | A stable name per replica, which labels the sandboxes it creates. |
| `ha.actors.host`<br />`HA_ACTORS_HOST` | `127.0.0.1` | The address other replicas reach this replica at. |
| `ha.actors.port`<br />`HA_ACTORS_PORT` | `7571` | The UDP port of the connections between replicas. |

[High availability](../high-availability/) walks through a complete setup.

## Advanced options

Most installs leave these alone.

- `app.env` (`APP_ENV`), default `production`.
  Keep it.
  With `development` or `test`, Umpteenth starts without an encryption key and uses a built-in, insecure one.
- `sandbox.adapter` (`SANDBOX_ADAPTER`), default `docker`, which drives Podman as well.
  `none` starts Umpteenth without a sandbox backend.
- `sandbox.broker_host` (`SANDBOX_BROKER_HOST`) applies when you run the `umpteenth` binary on a host instead of in a container.
  A relay container then forwards sandbox traffic to this address, `host.docker.internal` by default and `host.containers.internal` on Podman.
- `ha.actors.bind_address` (`HA_ACTORS_BIND_ADDRESS`) is the interface the peer connections listen on.
  It defaults to every interface with HA and to `ha.actors.host` without.
