# Site map

Every topic has one owning page that explains it in full. Other pages mention it in a sentence and link to the owner.
The sidebar order lives in `docs/astro.config.mjs`. Paths are relative to `docs/src/content/docs/`.

## Start here

| Page | Owns |
|---|---|
| `getting-started/introduction.mdx` | What Umpteenth is, the explore → assisted → scripted loop in brief, the core concepts, where credentials live in brief |
| `getting-started/installation.mdx` | Requirements, the compose file and a minimal `config.yml` for the published images, `app.url` and when the reverse proxy comes first, choosing how to sign in, starting, signing in, connecting the first model |
| `getting-started/first-job.mdx` | A walkthrough of a first job with no integrations, the Hacker News digest the screenshot kit seeds: describe, review the spec, run, read the run, see what it learned |

## Guides

| Page | Owns |
|---|---|
| `guides/writing-instructions.mdx` | What the agent and the compile step read, a checklist for good instructions, example jobs |
| `guides/jobs.mdx` | The job page and its tabs, editing a job, the model per job, what happens at each run limit, stopping automatic runs, the mode pin, job state, deleting |
| `guides/triggers.mdx` | Run now, schedules and time zones, webhooks and their tokens, the API trigger in brief, overlap policies, concurrency |
| `guides/runs.mdx` | The runs list, run statuses, the run page and its tabs, outputs and artifacts, cancelling, retrying, deleting, retention, the dashboard |
| `guides/self-improvement.mdx` | The playbook, reflection, changes held for review, graduation, scripted runs, fallback, demotion, shadow runs, rollback |
| `guides/sandboxes.mdx` | The sandbox, the default image, custom Dockerfiles and builds on the Environment tab, running as root, network modes, secrets, files and paths |
| `guides/mcp.mdx` | HTTP and stdio MCP servers, OAuth logins and auth badges, testing, attaching servers and allowing tools per job |
| `guides/models.mdx` | Providers, Claude Code and Codex subscriptions through CLIProxyAPI, where model lists come from, syncing, enabling models, prices, the three default model roles, run costs and showing usage as tokens, cost limits and the daily spend cap, long-run summaries |
| `guides/notifications.md` | The notification webhook, events, the payload, retries, signatures |
| `guides/workspaces.md` | One workspace or several (`workspaces.enabled`), roles, invite links and email invites, managing members, switching, leaving and deleting workspaces, the admin area and deactivating users |

## Self-hosting

| Page | Owns |
|---|---|
| `deployment/configuration.md` | How settings reach the server and the complete list of settings with defaults |
| `deployment/sign-in.mdx` | Which sign-in provider to pick, provider IDs and redirect URIs, OpenID Connect and GitHub setup, who may sign in, several providers on the login page, naming instance admins |
| `deployment/reverse-proxy.mdx` | HTTPS, `APP_URL`, trusting the proxy, keeping server-sent events open, Caddy, nginx and Traefik examples |
| `deployment/security.mdx` | Hardening, who can sign in, sessions and what ends them, the Docker socket, sandbox defaults, the network matrix, the egress proxy, `network.blocked_targets`, the unrestricted network, credentials, outbound calls |
| `deployment/backups.md` | Full backups and restores, and restoring on a new host |
| `deployment/upgrading.mdx` | Upgrading with `docker compose pull` plus the sandbox image pull, image tags and pinning, rolling back, what survives a restart, background maintenance in brief, health checks, logs |
| `deployment/gvisor.md` | Installing and turning on gVisor, what changes under it |
| `deployment/kubernetes.mdx` | The Helm chart: installing, the encryption key, the database, sandbox pods and their differences from Docker, network policies and the startup check, cluster ranges, job images with BuildKit, replicas and failover, replicas without Kubernetes, upgrades |

## Reference

| Page | Owns |
|---|---|
| `reference/job-settings.md` | Every field of a job's Settings tab with label, default and effect, while Sandboxes owns the Environment tab |
| `reference/api.md` | API tokens, errors, triggering and reading runs, events and streams, artifacts, job webhooks; every route and its parameters are on the generated API endpoints page |
| `reference/api-endpoints/` | Every API route on one page, rendered from `src/generated/openapi.json` by `src/pages/reference/api-endpoints/index.astro` with the components in `src/components/api/` and the styles in `src/styles/api.css`. `docs/scripts/openapi.mjs` writes the spec from the backend before every build and dev server, so fix a route's summary or field descriptions in the backend's operation. The page's intro and its links from routes to guide sections live in the page itself, and the sidebar links to it once |
| `reference/ump-cli.md` | The `ump` CLI inside sandboxes: commands, paths, environment |
| `reference/server-cli.md` | The `umpteenth` binary's commands and flags |

## Pages outside the content folder

- `docs/src/pages/index.astro` with `docs/src/components/landing/*.astro`: the landing page, with product claims that must stay true.
- `docs/src/components/diagrams/*.astro`: the diagram components, see [visuals.md](visuals.md).
- `docs/src/assets/screens/*.png`: the app screenshots, see [visuals.md](visuals.md).
