---
title: Configuration
description: Set Umpteenth's options in config.yml or through environment variables, with every option and its default.
---

You configure Umpteenth in a YAML file, `config.yml`, and override single options with environment variables.
Umpteenth reads both at start, so every change needs a restart.

## The config file

`docker-compose.yml` mounts `config.yml` from its own directory into the container at `/app/config.yml`, where Umpteenth looks for it.
Options you leave out or leave empty keep their defaults, so a short file is enough, and [Options](#options) lists every one:

```yaml title="config.yml"
app:
  url: https://umpteenth.example.com
  encryption_key: "<output of openssl rand -base64 32>"
```

To sign in at all, you also need a provider under `auth.providers`, which [Sign-in](../sign-in/) sets up.
Create the file before the first start, or Docker creates an empty directory named `config.yml` in its place ([Troubleshooting](../troubleshooting/#appencryption_key-app_encryption_key-is-required)).

Restart Umpteenth after an edit to the file:

```bash
docker compose restart umpteenth
```

To keep the file somewhere else in the container, point `CONFIG_FILE` or the `--config` flag at it.
An unknown option or an invalid value stops Umpteenth at start with an error that names it, and [Troubleshooting](../troubleshooting/#starting-up) lists the common ones.

## Environment variables

Each option has an environment variable named after its path, in upper case with underscores for dots and hyphens, so `server.trust_proxy` is `SERVER_TRUST_PROXY` and `auth.providers.pocket-id.allowed_groups` is `AUTH_PROVIDERS_POCKET_ID_ALLOWED_GROUPS`.
A variable wins over `config.yml`.
Set variables in the `environment:` block of the service:

```yaml title="docker-compose.yml"
services:
  umpteenth:
    environment:
      LOG_LEVEL: debug
      AUTH_PROVIDERS_POCKET_ID_ALLOWED_GROUPS: admins,ops
```

Apply a change to `environment:` with `docker compose up -d`, which recreates the container.
`docker compose restart` keeps the old environment.
An empty variable counts as unset and keeps the value from the file.
Lists take commas, as in `admins,ops`, and durations take a unit, such as `90m` or `12h`.

Docker Compose uses a `.env` file next to `docker-compose.yml` only to fill `${...}` placeholders in the compose file, and passes none of it to Umpteenth.
To pass its variables on, add `env_file: .env` to the service.

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

## Workspace defaults

Three options set defaults for fields you can also change under **Settings → General**:

| Option | Field |
|---|---|
| `sandbox.image` | **Default image** on the **Sandbox defaults** card |
| `runs.daily_spend_limit_usd` | **Daily spend limit** on the **Spend and retention** card |
| `runs.retention_days` | **Retention** on the **Spend and retention** card |

A workspace follows the option until one of its admins saves the card that holds the field.
From then on the workspace keeps its own value, and edits to the option leave it alone.

## Options

Each row shows the option's path in `config.yml` above its environment variable.

### App

| Option | Default | Description |
|---|---|---|
| `app.url`<br />`APP_URL` | `http://localhost:8080` | The URL you open Umpteenth at, which sign-in redirect URIs and links in notifications start with. With an `https://` URL, Umpteenth marks its cookies `Secure`, see [Reverse proxy](../reverse-proxy/). |
| `app.encryption_key`<br />`APP_ENCRYPTION_KEY` | none, required | At least 16 bytes, such as the output of `openssl rand -base64 32`. Encrypts secrets, provider API keys and MCP logins, and signs sessions. Under a different key, Umpteenth can't decrypt what it stored and everyone has to sign in again, so keep a copy with your [backups](../backups/). |
| `app.data_dir`<br />`APP_DATA_DIR` | `data`, which is `/app/data` in the container | Holds the SQLite database and stored files, unless `database.connection_string` or `file_storage.path` point elsewhere. |
| `app.env`<br />`APP_ENV` | `production` | Keep `production`. The other values are for developing Umpteenth. |

### Sign-in

Each sign-in provider has these options below its ID, and [Sign-in](../sign-in/) shows how to set up both types.
An option marked for one type stops Umpteenth at start on a provider of the other type.

| Option | Default | Description |
|---|---|---|
| `auth.providers.<id>.type`<br />`AUTH_PROVIDERS_<ID>_TYPE` | none, required | `oidc` for an OpenID Connect identity provider, or `github` for GitHub accounts. |
| `auth.providers.<id>.name`<br />`AUTH_PROVIDERS_<ID>_NAME` | none, required | The label of the provider's button, which reads **Sign in with** and the name. |
| `auth.providers.<id>.icon`<br />`AUTH_PROVIDERS_<ID>_ICON` | empty | An image for the button, as an `http://` or `https://` URL or a `data:image/` URI. |
| `auth.providers.<id>.primary`<br />`AUTH_PROVIDERS_<ID>_PRIMARY` | `false` | Gives the provider the large button at the top of the login page. At most one provider can be primary. |
| `auth.providers.<id>.client_id`<br />`AUTH_PROVIDERS_<ID>_CLIENT_ID` | none, required | The client ID of the OpenID Connect client or the GitHub OAuth app. |
| `auth.providers.<id>.client_secret`<br />`AUTH_PROVIDERS_<ID>_CLIENT_SECRET` | empty, required for `github` | The client secret. A public OpenID Connect client works without one. |
| `auth.providers.<id>.issuer`<br />`AUTH_PROVIDERS_<ID>_ISSUER` | none, required for `oidc` | `oidc` only. The issuer URL of the identity provider, which has to work from inside the container. |
| `auth.providers.<id>.allowed_groups`<br />`AUTH_PROVIDERS_<ID>_ALLOWED_GROUPS` | empty | `oidc` only. Groups whose members may sign in, spelled as in the `groups` claim, case included. Empty admits everyone the identity provider lets through. |
| `auth.providers.<id>.admin_groups`<br />`AUTH_PROVIDERS_<ID>_ADMIN_GROUPS` | empty | `oidc` only. Groups whose members are [instance admins](../sign-in/#instance-admins). |
| `auth.providers.<id>.allowed_users`<br />`AUTH_PROVIDERS_<ID>_ALLOWED_USERS` | empty | `github` only. GitHub usernames that may sign in. A `github` provider needs this list, `allowed_organizations` or an admin list. |
| `auth.providers.<id>.allowed_organizations`<br />`AUTH_PROVIDERS_<ID>_ALLOWED_ORGANIZATIONS` | empty | `github` only. GitHub organizations whose active members may sign in. |
| `auth.providers.<id>.admin_users`<br />`AUTH_PROVIDERS_<ID>_ADMIN_USERS` | empty | `github` only. GitHub usernames of [instance admins](../sign-in/#instance-admins). |
| `auth.providers.<id>.admin_organizations`<br />`AUTH_PROVIDERS_<ID>_ADMIN_ORGANIZATIONS` | empty | `github` only. GitHub organizations whose active members are instance admins. |

### Workspaces

| Option | Default | Description |
|---|---|---|
| `workspaces.enabled`<br />`WORKSPACES_ENABLED` | `false` | Lets people create workspaces, invite others and switch between them. Off, everyone who signs in shares one workspace, see [Workspaces](../../guides/workspaces/). |

### Server

| Option | Default | Description |
|---|---|---|
| `server.host`<br />`SERVER_HOST` | `0.0.0.0` | The interface both listeners bind to. Keep the default in a container. |
| `server.port`<br />`SERVER_PORT` | `8080` | The UI, the API and job webhooks. |
| `server.broker_port`<br />`SERVER_BROKER_PORT` | `8081` | The broker, which the `ump` CLI inside sandboxes calls, and the [egress proxy](../security/#the-egress-proxy) of their outbound connections. Keep it unpublished. |
| `server.trust_proxy`<br />`SERVER_TRUST_PROXY` | `false` | Takes the client address for the login rate limit from `X-Forwarded-For`. Turn it on only behind a [reverse proxy](../reverse-proxy/#trust-the-proxy). |

### Logs

| Option | Default | Description |
|---|---|---|
| `log.level`<br />`LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. At `debug` Umpteenth logs every API request, at `info` the failed ones. |
| `log.json`<br />`LOG_JSON` | `false` | JSON lines instead of text. |

### Database

| Option | Default | Description |
|---|---|---|
| `database.connection_string`<br />`DATABASE_CONNECTION_STRING` | `<app.data_dir>/umpteenth.db` | A SQLite file path, or a Postgres URL starting with `postgres://` or `postgresql://`, such as `postgres://umpteenth:secret@db:5432/umpteenth?sslmode=disable`. |

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
| `sandbox.adapter`<br />`SANDBOX_ADAPTER` | `docker` | `docker`, which drives Podman as well, or `none` for a replica that executes no runs. |
| `sandbox.image`<br />`SANDBOX_IMAGE` | `ghcr.io/stonith404/umpteenth-sandbox:latest` | The workspace default of **Default image**. Umpteenth pulls it at start if the engine lacks it. |
| `sandbox.allow_unrestricted_network`<br />`SANDBOX_ALLOW_UNRESTRICTED_NETWORK` | `true` | Offers jobs the **Unrestricted network**, which bypasses the egress proxy and reaches everything the host can, see [Security](../security/#the-unrestricted-network). |
| `sandbox.docker.runtime`<br />`SANDBOX_DOCKER_RUNTIME` | `runc` | The runtime of sandbox containers, `runsc` for [gVisor](../gvisor/). |
| `sandbox.docker.dns`<br />`SANDBOX_DOCKER_DNS` | empty, `8.8.8.8` and `8.8.4.4` under gVisor | The resolvers of **Unrestricted network** sandboxes in place of Docker's embedded DNS, which gVisor can't reach. Set it only together with `runsc`. |
| `sandbox.registry.repository`<br />`SANDBOX_REGISTRY_REPOSITORY` | empty | A registry path without a scheme, such as `ghcr.io/acme/umpteenth-jobs`, where [replicas](../high-availability/) share the images of jobs with a Dockerfile. Umpteenth never deletes tags there, so give it a cleanup policy. |
| `sandbox.registry.username`<br />`SANDBOX_REGISTRY_USERNAME` | empty | The user for pushes and pulls. |
| `sandbox.registry.password`<br />`SANDBOX_REGISTRY_PASSWORD` | empty | The password or token of that user. |
| `sandbox.broker_host`<br />`SANDBOX_BROKER_HOST` | `host.docker.internal`, or `host.containers.internal` on Podman | The address a relay container forwards sandbox traffic to. It applies only when the `umpteenth` binary runs outside a container. |

Umpteenth connects to Docker or Podman through the standard Docker client variables such as `DOCKER_HOST`, and through the socket at `/var/run/docker.sock` without them.

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
| `network.blocked_targets`<br />`NETWORK_BLOCKED_TARGETS` | empty | IP addresses and CIDR ranges that neither Umpteenth's own calls nor the egress proxy of sandboxes connect to, such as the host's own public address. They stay blocked with `network.allow_private_targets` and **Allow private network**, see [Security](../security/#the-egress-proxy). |

### Models

| Option | Default | Description |
|---|---|---|
| `models.catalog_refresh_interval`<br />`MODELS_CATALOG_REFRESH_INTERVAL` | `12h` | The interval at which Umpteenth downloads the [models.dev](https://models.dev) catalog and syncs every provider's model list, see [Models and costs](../../guides/models/#keeping-the-list-current). `0` turns the refresh off, and any other value must be at least `5m`. |

### High availability

| Option | Default | Description |
|---|---|---|
| `ha.enabled`<br />`HA_ENABLED` | `false` | Lets several replicas share one Postgres database, and needs a Postgres `database.connection_string`. |
| `ha.replica_id`<br />`HA_REPLICA_ID` | the hostname | A stable name per replica, which labels the sandboxes it creates. |
| `ha.actors.host`<br />`HA_ACTORS_HOST` | `127.0.0.1` | The address other replicas reach this replica at. |
| `ha.actors.port`<br />`HA_ACTORS_PORT` | `7571` | The UDP port of the connections between replicas. |
| `ha.actors.bind_address`<br />`HA_ACTORS_BIND_ADDRESS` | every interface with HA, `ha.actors.host` without | The interface the connections between replicas listen on. |

[High availability](../high-availability/) walks through a complete setup.
