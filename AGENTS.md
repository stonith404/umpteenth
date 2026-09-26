- `backend/` — Go module
- `frontend/` — SvelteKit 5 SPA. Builds into `backend/frontend/dist` and is embedded via `go:embed`.
- `tests/` — Playwright end-to-end tests (drives a Dockerized full stack).
- `docs/` — Astro Starlight documentation site; `pnpm docs` serves it, `pnpm docs:build` builds it. Pushes to `main` deploy it to https://umpteenth.dev on Cloudflare Workers (`docs/wrangler.jsonc`, `.github/workflows/docs.yml`).


## Configuration

The server reads an optional YAML file (`config.yml` in the working directory, or `--config` / `CONFIG_FILE`) and then the environment, which wins.
`backend/internal/config/config.go` is the schema: the `yaml` tags name the options and the `default` tags hold their defaults.
Environment variable names are derived from the YAML path (`server.port` → `SERVER_PORT`, plus a `_FILE` variant), so never hardcode one in Go code; refer to options by YAML path, and add every new option to `config.example.yml`, which a unit test checks.


## Testing the UI without signing in

Login is OIDC only, so a normal build redirects every page to the identity provider.
Test builds skip that: the `e2etest` build tag adds test-only routes, which are mounted only when `APP_ENV` is not `production`.
`POST /api/test/session` signs in as an "E2E User" and answers with a regular session cookie.

1. Start a backend built with the `e2etest` tag, either way works:
   - The e2e stack, which also serves the UI on http://localhost:8080: `docker compose -f tests/setup/docker-compose.yml up -d --build`
   - The dev backend with the extra tag, with the UI from `pnpm dev` on http://localhost:3000: `cd backend && APP_ENV=development go run -tags exclude_frontend,exclude_ump,e2etest ./cmd/umpteenth`
2. Open the app in the browser, run `await fetch('/api/test/session', { method: 'POST' })` in the page, and reload.
   It must be a POST, so opening the URL does nothing.
   From a script, `curl -c cookies.txt -X POST <url>/api/test/session` gives a cookie jar to send along.

Caveats:
- Port 8080 is often taken by a running dev backend. Run the e2e stack on another port with an override file passed as a second `-f`, which sets `ports: !override ["18080:8080"]` and `APP_URL: http://localhost:18080` on the `umpteenth` service. For the dev backend, set `SERVER_PORT`, `SERVER_BROKER_PORT` and `HA_ACTORS_PORT`, and point `pnpm dev` at it with `DEVELOPMENT_BACKEND_URL`.
- Test builds also expose `POST /api/test/reset`, which wipes the users, workspaces, providers, secrets, jobs, runs and settings. Never run one against data you care about; give it its own `APP_DATA_DIR`, and set `CONFIG_FILE=/dev/null` so it doesn't pick up a `backend/config.yml` that points at another database.
- Test builds add a fake LLM provider whose answers `POST /api/test/llm-script` scripts (see `tests/utils/run.util.ts`), so runs don't need a real model.


## Coding Style Guidelines

### Comments

- Exactly one sentence per line
- There is NO maximum line width: never wrap a single sentence across multiple comment lines, no matter how long that sentence is
- A new line in a comment means a new sentence; a wrapped line does not exist
- No trailing period on single-line comments
- Prefer comments that explain intent, invariants, or why a branch exists
- Avoid comments that simply restate the next line of code
- For multi-step logic, use short section comments to separate the steps and explain why each step exists
- Inside a function, put a one-sentence comment above each major action; the comments double as visual separators between sections and should say what the step does and why, not how
- Favor a few well-placed section comments over a wall of code; a reader should be able to skim the comments and understand the method's flow

```go
// Wrong — one sentence wrapped across multiple lines
// This function performs the main validation logic. It checks
// the input against the schema and returns an error if the
// input is invalid.

// Wrong — trailing period on single-line comment
// Validate the input.

// Right — one sentence per line, each line as long as it needs to be
// This function performs the main validation logic
// It checks the input against the schema and returns an error if the input is invalid

// Right
// Validate the input

// Right
// Normalize the request host so callers can pass either Host or X-Forwarded-Host values

// Right
// Browsers do not accept a cookie Domain attribute set to an IP address
// Returning an empty domain tells the caller to set a host-only cookie instead

// Wrong — restates the code
// Trim whitespace and lowercase the host
host = strings.TrimSpace(strings.ToLower(host))
```

Section comments inside a function — one sentence per major action, describing what and why, acting as visual separators:
