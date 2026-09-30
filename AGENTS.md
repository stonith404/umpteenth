# Umpteenth

Self-hosted app for agentic jobs: each run gets a disposable sandbox where an LLM agent works with shell tools and MCP servers.
Setup and test commands for humans are in `CONTRIBUTING.md`.

## Layout

- `backend/` — Go server (Huma + sqlc), one package per feature in `internal/` (`module.go`, `handlers.go`, `service.go`, `queries.sql`), wired in `internal/bootstrap`.
- `frontend/` — SvelteKit 5 SPA, built into `backend/frontend/dist` and embedded via `go:embed`.
- `tests/` — Playwright end-to-end tests against a Dockerized stack (`tests/setup`).
- `charts/umpteenth/` — Helm chart for the `kubernetes` sandbox adapter.
- `docs/` — Astro Starlight site deployed to https://umpteenth.dev; use the `update-docs` skill to edit it.
  Its build also builds the app's run page as a demo (`frontend/src/demo`), so a frontend change can break it.

## Rules

- Commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:` …), since they decide the version bump and become the changelog.
- After changing the frontend, run `pnpm lint` and fix all errors; don't add new `@shadcn/lint` warnings, which name the variant or theme token to use instead.
- After changing the API, run `pnpm gen:api`; after changing a `queries.sql` or migration, run `make gen` in `backend/`.
  Migrations exist for both Postgres and SQLite in `backend/resources/migrations`.
- Backend unit tests: `make test` in `backend/`.
- `backend/internal/config/config.go` is the config schema (`yaml` and `default` tags).
  Refer to options by YAML path and never hardcode env var names, which are derived from it (`server.port` → `SERVER_PORT`).
  Document every new option on the docs' Configuration page, and add it to `config.example.yml` only if most installs set it, marked `# Required` or `# Recommended to change`.
- Every route declares its access rule with `httpserver.Restrict` and must be added to `internal/bootstrap/access_test.go`.
- `workspaces.enabled` is off by default, so everyone shares `workspaces.DefaultID`; turn it on to test the switcher, invites and the admin area.

## Testing the UI without signing in

Login is OIDC only, but a backend built with the `e2etest` tag (and `APP_ENV` not `production`) adds test-only routes:

- `POST /api/test/session` signs in and sets a session cookie; run `await fetch('/api/test/session', { method: 'POST' })` in the page and reload.
  An optional JSON body (`subject`, `email`, `name`, `admin` …) signs in as another user.
- `POST /api/test/llm-script` scripts a fake LLM provider, so runs don't need a real model (see `tests/utils/run.util.ts`).
- `POST /api/test/reset` wipes all data, so only ever run it on a throwaway instance.

Start one with `docker compose -f tests/setup/docker-compose.yml up -d --build` (UI on :8080), or with `cd backend && APP_ENV=development go run -tags exclude_frontend,exclude_ump,e2etest ./cmd/umpteenth` plus `pnpm dev` (UI on :3000).
Ports 8080 and 3000 are often taken by a running dev instance; move the backend with `SERVER_PORT`, `SERVER_BROKER_PORT` and `HA_ACTORS_PORT`, and point `pnpm dev` at it with `DEVELOPMENT_BACKEND_URL`.
Give a throwaway instance its own `APP_DATA_DIR` and `APP_ENCRYPTION_KEY`, and `CONFIG_FILE=/dev/null` so it ignores `backend/config.yml`.

## Comments

- Exactly one sentence per line, never wrapped, no matter how long.
- No trailing period on single-line comments.
- Explain intent, invariants and why a branch exists; never restate the next line.
- Inside a function, put a one-sentence comment above each major step, so skimming the comments explains the flow.

```go
// Browsers do not accept a cookie Domain attribute set to an IP address
// Returning an empty domain tells the caller to set a host-only cookie instead
```
