# Site map

Every topic has one owning page that explains it in full. Other pages mention it in a sentence and link to the owner.
The sidebar order lives in `docs/astro.config.mjs`. Paths are relative to `docs/src/content/docs/`.

## Start here

| Page | Owns |
|---|---|
| `getting-started/introduction.mdx` | What Umpteenth is, the explore → assisted → scripted loop in brief, the core concepts, where credentials live |
| `getting-started/installation.mdx` | Requirements, building the images, the OIDC client, the first configuration, starting, signing in, connecting the first model, checking the sandbox backend, Podman |
| `getting-started/first-job.mdx` | A walkthrough of a first job with no integrations: describe, review the spec, run, read the run, see what it learned |

## Guides

| Page | Owns |
|---|---|
| `guides/writing-instructions.mdx` | What the agent and the compile step read, a checklist for good instructions, example jobs |
| `guides/jobs.mdx` | The job page and its tabs, editing a job, the model per job, run limits, pausing, the mode pin, job state, deleting |
| `guides/triggers.mdx` | Run now, schedules and time zones, webhooks and their tokens, the API trigger in brief, overlap policies, concurrency, retries |
| `guides/runs.mdx` | The runs list, run statuses, the run page and its tabs, outputs and artifacts, cancelling, retrying, the dashboard |
| `guides/self-improvement.mdx` | The playbook, reflection, changes held for review, graduation, scripted runs, fallback, demotion, shadow runs, rollback |
| `guides/sandboxes.mdx` | The sandbox, the default image, custom Dockerfiles and builds, running as root, network modes, secrets, files and paths |
| `guides/mcp.mdx` | HTTP and stdio MCP servers, OAuth logins and auth badges, testing, attaching servers and allowing tools per job |
| `guides/models.mdx` | Providers, where model lists come from, syncing, enabling models, prices, the three default model roles, cost limits and the daily spend cap, long-run summaries |
| `guides/notifications.md` | The notification webhook, events, the payload, retries, signatures |

## Self-hosting

| Page | Owns |
|---|---|
| `deployment/configuration.md` | How settings reach the server and the complete list of settings with defaults |
| `deployment/reverse-proxy.mdx` | HTTPS, `APP_URL`, trusting the proxy, keeping server-sent events open, Caddy, nginx and Traefik examples |
| `deployment/security.mdx` | Hardening, who can sign in, the Docker socket, sandbox defaults, the network matrix, the egress firewall, credentials, outbound calls |
| `deployment/gvisor.md` | Installing and turning on gVisor, what changes under it |
| `deployment/backups.md` | Full backups and restores, export and import between instances |
| `deployment/upgrading.mdx` | Upgrading, what survives a restart, background maintenance, health checks, logs |
| `deployment/high-availability.mdx` | Several replicas: requirements, settings, the example compose file, failover, rolling upgrades |
| `deployment/troubleshooting.md` | Symptoms and exact error messages with their cause and fix, grouped by area |

## Reference

| Page | Owns |
|---|---|
| `reference/job-settings.md` | Every field of a job's Settings and Environment tabs with label, default and effect |
| `reference/api.md` | API tokens, the OpenAPI description, triggering and reading runs, events and streams, job webhooks, list parameters |
| `reference/ump-cli.md` | The `ump` CLI inside sandboxes: commands, paths, environment |
| `reference/server-cli.md` | The `umpteenth` binary's commands and flags |

## Pages outside the content folder

- `docs/src/pages/index.astro` with `docs/src/components/landing/*.astro`: the landing page, with product claims that must stay true.
- `docs/src/components/diagrams/*.astro`: the diagram components, see [visuals.md](visuals.md).
- `docs/src/assets/screens/*.png`: the app screenshots, see [visuals.md](visuals.md).
