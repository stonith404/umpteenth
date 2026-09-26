# Umpteenth

Umpteenth is a self-hosted app for agentic jobs you describe in plain language.
Each run gets its own disposable sandbox, where an LLM agent works with shell tools and the MCP servers you connect.
Jobs run on a schedule, from a webhook or on demand, and every run is recorded with a live timeline, its cost and its outputs.

> [!NOTE]
> Umpteenth is in early development.
> Milestones M0–M6 of [PLAN.md](PLAN.md) are implemented.

The documentation is at [umpteenth.dev](https://umpteenth.dev). Its source lives in [`docs/`](docs/src/content/docs), and `pnpm docs` serves it locally.

## Features

- **Plain-language jobs:** a compile step turns your description into a schedule, success criteria, outputs and the MCP servers the job needs, which you confirm before saving.
- **Triggers:** cron schedules with time zones, authenticated webhooks, and manual runs with input.
- **Jobs that learn:** with learning on, reflection turns every run into playbook changes: learnings for the next run, tested scripts the agent calls as tools, and installs moved into the job's Dockerfile. Every change is a version you can diff and roll back, and risky changes wait for review.
- **Graduation to scripts:** once a job has done the same steps three runs in a row, it graduates to a main script that runs without the agent and costs close to nothing. When the script fails its checks, the agent finishes the job in the same sandbox without repeating side effects, and reflection repairs the script.
- **Isolated sandboxes:** every run gets a fresh Docker or Podman container with resource limits and an optional gVisor runtime. The agent runs as a non-root user with all capabilities dropped unless a job opts into root.
- **Egress control:** internet sandboxes go out through an egress proxy that keeps them off private networks, the Docker host and cloud metadata, on Docker and Podman alike. Jobs can instead be limited to a list of domains, or get no network at all.
- **Per-job environments:** a job can carry its own Dockerfile. Its image is built once and cached, so tools aren't installed on every run.
- **MCP:** HTTP and stdio MCP servers with a per-job tool allow-list. HTTP credentials stay on the host, and stdio servers run as a separate user from the agent.
- **Any LLM:** the native Anthropic API and any OpenAI-compatible endpoint, such as OpenAI, OpenRouter, Ollama, vLLM or LM Studio.
- **Cost control:** per-run cost, turn and time limits, a daily workspace spend cap, and per-model pricing.
- **Job state:** a small key-value store that persists between a job's runs, e.g. for the last seen ID.
- **Notifications:** a webhook, signed if you like, for failed runs, fallbacks and demotions, which works with Slack and Discord as it is.
- **Export and import:** `umpteenth export` and `umpteenth import` move jobs, playbooks and settings between instances, without any secret values.
- **Long runs:** conversations that approach the model's context window are summarized and continued.
- **High availability:** optionally run several replicas on Postgres and S3-compatible storage.

## Requirements

- **Docker Engine or Podman** on the host. Umpteenth creates the sandboxes through the Docker API socket.
- **A way to sign in:** an OpenID Connect provider, such as [Pocket ID](https://github.com/pocket-id/pocket-id), Authentik, Keycloak or Zitadel, or GitHub accounts. There are no local passwords.
- **An LLM provider:** an Anthropic API key, or any OpenAI-compatible endpoint, including a local Ollama.

## Installation

The [installation guide](https://umpteenth.dev/getting-started/installation/) walks through every step. In short, on a host with Docker:

1. Create a directory with this `docker-compose.yml`:

   ```yaml
   services:
     umpteenth:
       image: ghcr.io/stonith404/umpteenth:latest
       container_name: umpteenth
       restart: unless-stopped
       ports:
         - "8080:8080"
       volumes:
         - ./config.yml:/app/config.yml:ro
         - ./data:/app/data
         - /var/run/docker.sock:/var/run/docker.sock
   ```

2. Create a `config.yml` next to it with the URL you open Umpteenth at, an encryption key from `openssl rand -base64 32`, and a way to sign in through an OpenID Connect provider or GitHub, as [Sign-in](https://umpteenth.dev/deployment/sign-in/) shows:

   ```yaml
   app:
     url: http://localhost:8080
     encryption_key: "<output of openssl rand -base64 32>"

   auth:
     providers:
       pocket-id:
         type: oidc
         name: Pocket ID
         issuer: https://id.example.com
         client_id: umpteenth
         client_secret: "<client secret>"
   ```

   Every other option has a default, see [Configuration](https://umpteenth.dev/deployment/configuration/).

3. Start it, open `app.url` and sign in:

   ```bash
   docker compose up -d
   ```

4. Add a model provider under **Settings → Providers & models**, pick the **Agent** model under **Settings → General → Default models**, and create your first job under **Jobs**.

## Security notes

- **The Docker socket is root on the host.** Umpteenth needs it to create sandboxes, so anyone who controls the Umpteenth container controls the host. Prefer a rootless Podman socket or a socket proxy limited to containers, exec, images, networks and build, set through `DOCKER_HOST`.
- **Stronger isolation:** set `sandbox.docker.runtime: runsc` to run sandboxes under [gVisor](https://gvisor.dev). The runtime needs two flags for Umpteenth, which [the gVisor guide](docs/src/content/docs/deployment/gvisor.md) explains, and a startup check refuses runs until they are set.
- **Sandbox egress:** internet sandboxes have no route of their own and reach the outside through an egress proxy in Umpteenth, which refuses private networks, the host and cloud metadata. Add the host's public IP addresses to `network.blocked_targets`, and set `sandbox.allow_unrestricted_network: false` unless a job needs the unrestricted network, which bypasses the proxy. See [Security](docs/src/content/docs/deployment/security.mdx).
- **Reverse proxy:** behind a proxy that sets `X-Forwarded-For`, set `server.trust_proxy: true` so login rate limits see client addresses. Never set it without such a proxy. The proxy must keep server-sent event (SSE) connections open for live run updates.

## High availability

[`docker-compose.ha.yml`](docker-compose.ha.yml) runs two replicas behind a load balancer, with Postgres and S3-compatible storage.
HA needs these settings on every replica:

| Option | Value |
|---|---|
| `database.connection_string` | `postgres://…` |
| `ha.enabled` | `true` |
| `file_storage.backend` | `s3` (with the `file_storage.s3` options) or `database` |
| `ha.actors.host` | A hostname the other replicas can reach this replica at |
| `ha.replica_id` | A stable, unique name per replica |
| `sandbox.registry.repository` | Optional: a registry for job images, so replicas share builds instead of each building a job's image on first use |

```bash
docker compose -f docker-compose.ha.yml up -d
```

## Configuration

Umpteenth reads `config.yml` from its working directory, which is `/app` in the container.
Another path can be given with `--config` or `CONFIG_FILE`.
[`config.example.yml`](config.example.yml) lists every option with its default and a short explanation.

Every option can also be set through an environment variable named after its path, which wins over the file:

| Option | Environment variable |
|---|---|
| `app.encryption_key` | `APP_ENCRYPTION_KEY` |
| `server.port` | `SERVER_PORT` |
| `file_storage.s3.bucket` | `FILE_STORAGE_S3_BUCKET` |

The hyphens of a sign-in provider's ID become underscores, e.g. `auth.providers.pocket-id.issuer` is `AUTH_PROVIDERS_POCKET_ID_ISSUER`.
Lists are comma-separated in environment variables, e.g. `AUTH_PROVIDERS_GITHUB_ALLOWED_USERS=octocat,hubot`.
Each variable also accepts a `_FILE` variant for Docker secrets, e.g. `APP_ENCRYPTION_KEY_FILE`.

See [Configuration](docs/src/content/docs/deployment/configuration.md) for the complete reference.

## Development

The repository has four parts:

- `backend/`: the Go server, which embeds the built frontend and the `ump` CLI injected into sandboxes.
- `frontend/`: the SvelteKit SPA.
- `docs/`: the documentation site, built with Astro Starlight.
- `tests/`: Playwright end-to-end tests against a Dockerized stack.

You need Go 1.27, Node.js 24, pnpm 10 and Docker.

```bash
pnpm install
```

The backend reads `backend/config.yml`, so copy the example there.
Set `app.env` to `development` and `app.url` to the frontend dev server, `http://localhost:3000`, register `http://localhost:3000/api/auth/callback/<id>` as a redirect URI, and fill in a sign-in provider under `auth.providers`.
Set `app.encryption_key` to a private random value such as the output of `openssl rand -base64 32`; development instances require it too because it signs sessions and encrypts stored credentials.
Environment variables in `backend/.env` are read too.

```bash
cp config.example.yml backend/config.yml
```

Start the backend.
The `exclude_frontend` tag skips the embedded UI, and `exclude_ump` compiles the `ump` CLI at runtime instead of embedding it.

```bash
cd backend && go run -tags exclude_frontend,exclude_ump ./cmd/umpteenth
```

Start the frontend on http://localhost:3000, which proxies API requests to the backend on port 8080:

```bash
pnpm dev
```

After changing the API, regenerate the OpenAPI spec and the TypeScript types:

```bash
pnpm gen:api
```

### Tests

Backend unit tests:

```bash
cd backend && make test
```

The sandbox conformance suite against the local Docker daemon:

```bash
cd backend && make test-integration
```

The same suite under gVisor, in a throwaway engine that leaves the local one untouched:

```bash
backend/scripts/gvisor-engine.sh
```

The Playwright suite drives a stack built with the `e2etest` tag, which adds a fake LLM provider and test-only routes:

```bash
docker compose -f tests/setup/docker-compose.yml up -d --build
```

```bash
pnpm test
```

The self-improvement eval runs six reference jobs several times each with learning on, with a real model on an OpenAI-compatible endpoint, and reports how turns, cost and duration develop.
It targets the e2e stack by default; set `APP_URL` and `API_TOKEN` to evaluate another instance.

```bash
EVAL_BASE_URL=https://api.openai.com/v1 EVAL_API_KEY=<key> EVAL_MODEL=<model> pnpm --filter umpteenth-tests eval
```

## License

Umpteenth is licensed under the [GNU Affero General Public License v3.0](LICENSE).
Anyone who runs a modified version as a network service must offer its users the complete source code of that version under the same license.
