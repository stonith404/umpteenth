# Umpteenth: Project Plan

> Status: planning · Last updated: 2026-09-25

Umpteenth is a self-hosted app for **agentic jobs you describe in plain language**. Each run gets its own disposable sandbox. Jobs can use MCP servers. They also **get cheaper and faster over time**: every run feeds a per-job *playbook* of learnings and scripts, and a job can gradually *graduate* from a full LLM agent to a script with a small amount of LLM help.

---

## 0. Decisions so far

| Topic | Decision |
|---|---|
| Backend | Go, one static binary, frontend embedded. Code structure follows **Pocket ID** (feature modules, `bootstrap`, `apperror`, `common.EnvConfig`, build tags), see §3.2 |
| Frontend | SvelteKit (static SPA) + Svelte 5 + Tailwind v4 + shadcn-svelte + **TanStack Table** (fully server-side), with Pocket ID's frontend conventions (§13). **English only** for v1 |
| API | **Huma** (standalone, `net/http`) → OpenAPI 3.1 → generated TS types + `openapi-fetch` |
| Storage | **SQLite** (default, single node) **or Postgres** (required for HA), like Pocket ID. goose migrations per engine, **sqlc** from one portable query file per module (§11). Blobs via Pocket ID's `storage.FileStorage` (filesystem, S3, database) |
| Background work | **Francis** actors (embedded runtime, state in the app database): job schedules, concurrency policies, the run queue (`taskpool`), image builds, cron jobs, signals, rate limits (§3.3) |
| High availability | **HA-ready by construction**: all durable state in the database, all async work in Francis. HA = Postgres + `HA_ENABLED=true` + more replicas, not a code change (§3.4) |
| SaaS readiness | Every tenant-owned row carries `workspace_id` from day one. v1 ships a single default workspace; sign-up, roles and billing come later without a rewrite (§3.5) |
| Sandbox | **Generic `sandbox.Adapter` interface**, one adapter active per instance (`SANDBOX_ADAPTER`). v1 adapter: **Docker / Podman** via the Engine API, optional **gVisor (`runsc`)**. Designed for later local-runtime (Firecracker, Kata, Apple `container`, Lima) and Kubernetes adapters (§4) |
| Job environment | **Per-job Dockerfile**, versioned in the playbook, built once and cached, so tools aren't installed on every run (§4.11) |
| Broker | HTTP API on its own listener. **How the sandbox reaches it is the adapter's job** (§4.5) |
| LLM | **Provider-agnostic from day one**: our own thin `Provider` interface. Adapters: native Anthropic, plus OpenAI-compatible (OpenAI, Ollama, OpenRouter, vLLM, LM Studio, …) |
| LLM + script | **Graduated runs**: Explore → Assisted → Scripted, with the LLM as fallback and verifier |
| Self-improvement | **Per-job toggle.** When on, changes apply automatically. Run and playbook history is always saved. Every playbook change is a version you can roll back |
| Job definition | Language only. A "compile" step pulls schedule, MCP needs and success criteria out of the text for you to confirm |
| Auth | **OIDC only**, plus API tokens for webhooks/automation. No local password |

---

## 1. Goals, non-goals, "lightweight" budgets

**Goals**
1. You describe a job in plain language, and it runs on a schedule, on a webhook, or on demand in an isolated sandbox.
2. Jobs can use shell tools inside the sandbox and any registered MCP server.
3. Each job learns from its runs: edge cases, reusable scripts, its own environment, and eventually a script-first fast path.
4. A clean UI shows every run: what happened, how long each part took, and what it cost.
5. It runs on a single node with no database server, or as several replicas for HA. The architecture can grow into a hosted multi-tenant SaaS.

**Non-goals for v1:**
- SaaS product features: sign-up, roles, billing, a workspace UI. The schema and code paths are ready for them (§3.5).
- Hosted sandbox services (E2B, Daytona, …).
- A GUI/browser inside the sandbox. It could run as an MCP server later.
- Building Kubernetes and microVM sandboxes. They are *not* in v1, but the sandbox interface is designed so they can be added without touching the core.

**Lightweight targets (measurable)**
| Metric | Target |
|---|---|
| Binary size (incl. embedded UI + `ump` CLI for 2 archs) | < 50 MB |
| Idle RSS (per replica) | < 60 MB |
| Run start overhead (Docker adapter, job image cached) | < 1 s |
| External runtime dependencies | The active sandbox adapter's backend (v1: a Docker **or** Podman socket) and an OIDC provider. SQLite by default, so no database server; Postgres only for HA. No Redis, no queue |

---

## 2. Concepts & glossary

| Term | Meaning |
|---|---|
| **Workspace** | The tenant boundary. Every job, run, secret, provider, MCP server and token belongs to one. v1 has exactly one |
| **Job** | A plain-language instruction plus settings (model, image, limits, MCP servers, triggers, self-improve toggle) |
| **Spec** | Structured data *compiled* from the instruction (schedule, success criteria, inputs/outputs, MCP needs). You can override any field |
| **Run** | One execution of a job in one sandbox. Its full event timeline is always stored |
| **Playbook** | What the job has learned, versioned: **learnings** (text), **toolkit** (scripts), **Dockerfile**, **setup** script, **main** script + **verify** spec (after graduation) |
| **Job image** | The sandbox image built from the playbook's Dockerfile (§4.11), cached and reused across runs |
| **Reflection** | A post-run LLM step that turns a run transcript into playbook operations (add learning, add script, …) |
| **Mode** | How a run is driven: **Explore** (empty playbook, full agent), **Assisted** (agent + playbook), **Scripted** (main script, with the LLM only for verification or fallback) |
| **Job state** | Key-value data kept across runs (e.g. "last seen item ID"). This is *data*; the playbook is *know-how* |
| **Sandbox adapter** | An implementation of `sandbox.Adapter` for one isolation backend (Docker, later Kubernetes, Firecracker, …). Exactly one is active per instance |
| **Broker** | Host-side API the sandbox can reach, used by the `ump` CLI for MCP calls, LLM calls, outputs and state. Credentials stay on the host |
| **`ump` CLI** | Small static binary copied into every sandbox. It is how scripts use MCP and LLMs without an agent loop |

---

## 3. Architecture

### 3.1 Overview

```
┌──────────────────────────── umpteenth (single Go binary) ─────────────────────────────┐
│                                                                                       │
│  HTTP API (Huma, OpenAPI 3.1) ── SSE ──► Svelte UI (embedded)     OIDC login ──► IdP  │
│      │                                                                                │
│  Francis actor host (embedded, state in the app DB, clusters across replicas)         │
│   ├─ job actors: schedule alarm + concurrency policy ──► runs taskpool (N / replica)  │
│   ├─ taskpools: reflection, image builds                      │                       │
│   ├─ cron jobs: run reconciler, retention, image GC           │                       │
│   └─ signals: run cancel · rate limits: webhooks, login       │                       │
│                                               ┌───────────────┴───────────────┐       │
│                                               │ Runner (per run)              │       │
│                                               │  mode = explore|assisted|     │       │
│                                               │         scripted              │       │
│                                               │  ├─ Agent loop ◄──► llm.Provider      │
│                                               │  ├─ Tools ──► sandbox / MCP / toolkit │
│                                               │  ├─ Verifier                          │
│                                               │  └─ Event recorder ──► DB + event bus │
│                                               └───────────────┬───────────────┘       │
│  MCP manager (HTTP servers from host,                         │                       │
│   stdio servers inside sandbox)                               │                       │
│  Broker API (:8081, per-run token) ◄──────────────────────────┼──────┐                │
│  FileStorage (filesystem | S3 | DB): outputs, artifacts, logs │      │                │
│  sandbox.Adapter (SANDBOX_ADAPTER=docker; later k8s, microVM) │      │                │
└───────────────────────────────────────────────────────────────┼──────┼────────────────┘
                                                                ▼      │ path chosen by the adapter
                                                  ┌────────────────────┴───────┐
                                                  │ Sandbox (per run)          │
                                                  │  job image (Dockerfile)    │
                                                  │  /workspace   (agent cwd)  │
                                                  │  /ump/toolkit (playbook)   │
                                                  │  /ump/input.json           │
                                                  │  /ump/outputs/ (collected) │
                                                  │  /usr/local/bin/ump ───────┼──► Broker
                                                  │  stdio MCP servers (mcp)   │
                                                  └────────────────────────────┘
```

**Key principle: the agent loop runs on the host, and the sandbox only executes.** The sandbox never holds LLM API keys or remote MCP credentials. A compromised sandbox can do only what that run is already allowed to do, through the broker, with a token that expires when the run ends.

### 3.2 Code structure (Pocket ID conventions)

The backend copies Pocket ID's newer **feature-module** style. It does not copy Pocket ID's older `controller/`, `service/`, `dto/` and `model/` layer packages, and it does not use Gin or GORM (Huma and sqlc instead).

**Feature module anatomy** (e.g. `backend/internal/jobs/`):
```
jobs/
├── module.go        # Dependencies struct, Module, New(ctx, deps), RegisterRoutes(api, auth)
├── handler.go       # Huma operations: thin, map DTOs ↔ service calls
├── service.go       # business logic (exported Service)
├── dto.go           # request/response types with Huma validation tags (unexported)
├── models.go        # domain types
├── queries.sql      # portable sqlc queries owned by this module (§11)
├── jobsdb/          # sqlc output (generated, never edited)
├── list.go          # listquery whitelist for this module's table endpoints (§12.1)
├── actor.go         # Francis actor(s) owned by this module, if any
└── service_test.go
```

```go
type Dependencies struct {
    DB       *database.DB     // SQLite or Postgres; implements sqlc's DBTX (§11)
    Actors   francishost.Host
    Compiler SpecCompiler     // narrow interface, declared here and satisfied by another module
}

func New(ctx context.Context, deps Dependencies) (*Module, error)

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
    huma.Register(api, huma.Operation{
        OperationID: "list-jobs", Method: http.MethodGet, Path: "/api/jobs",
        Tags: []string{"Jobs"}, Middlewares: auth,
    }, m.handler.list)
}

// Tenant-owned data is always scoped explicitly, the way Pocket ID passes userID
func (s *Service) Get(ctx context.Context, workspaceID, jobID string) (Job, error)
```

**Rules**
- A module exposes only `Module` and the few methods other modules need. Its dependencies come in through the `Dependencies` struct and are used via **small interfaces declared by the consumer**, as Pocket ID does with e.g. `APIKeyExpiryEmailSender`.
- `internal/bootstrap` is the only package that knows every module. It does the following, then runs everything with go-kit `servicerunner` (HTTP server, broker listener, actor host) plus a shutdown manager:
  - opens the DB and file storage
  - starts the actor host
  - picks the sandbox adapter
  - builds the modules in dependency order (`initServices`)
  - mounts the routes (`initRouter`)
- **Engine packages** (`sandbox`, `llm`, `agent`, `runner`, `mcp`, `events`) have no HTTP routes and know nothing about modules.
- **Errors:** `internal/apperror`, copied from Pocket ID.
  - Services return `*apperror.Error` for client-visible failures (stable `code`, HTTP status, safe message, field errors), and wrap everything else with `fmt.Errorf("…: %w", err)`.
  - `apperror.Error` implements `huma.StatusError`, and a custom `huma.NewError` renders every error as `{code, message, fields, requestId}`.
  - Unknown errors become `internal_error`; the cause is logged but not sent to the client.
- **Config:** `common.EnvConfig` via `caarlos0/env`. Every secret supports a `*_FILE` variant. `APP_ENV` = `production | development | test`.
- **Logging:** stdlib `log/slog` only.
- **HA and tenancy rules** (§3.4, §3.5) apply to every module and are copied into `AGENTS.md` when the repo is set up.
- **CLI:** cobra in `internal/cmds`: `serve` (default), `healthcheck`, `openapi` (prints the spec for TS generation), `export`/`import` (M6), `version`.
- **Build tags**, as in Pocket ID:
  - `exclude_frontend`: skip the embedded UI (local dev)
  - `exclude_ump`: build the `ump` binary on first use instead of embedding it (local dev)
  - `unit`: unit tests
  - `integration`: real-backend sandbox conformance tests
  - `e2etest`: test-only endpoints and the fake LLM provider

### 3.3 Background work (Francis)

As in Pocket ID, the Francis runtime is embedded and uses the app database as its provider (SQLite or Postgres). Its tables are managed by Francis, not by our migrations.
- **SQLite:** a single host (`maxHosts = 1`); a second replica refuses to start.
- **Postgres with `HA_ENABLED=true`:** any number of replicas join the cluster. Actors, alarms, taskpool work and cron jobs are distributed across them, and fail over when a replica dies.

| Actor | Kind | Responsibility |
|---|---|---|
| `job/<jobId>` | custom actor | Owns the job's **schedule alarm** (next cron occurrence in the job's timezone) and its **concurrency policy**. Every trigger (schedule, manual, webhook, API) goes through it, so policy decisions are serialized per job, whichever replica received the trigger. `skip` records a skipped run, `queue` holds the trigger until the active run finishes, and `parallel` submits right away. It is rescheduled on job edit, enable or disable, and mirrors the alarm into `jobs.next_run_at` for the UI. On activation it reconciles its active-run set against the `runs` table |
| `runs` | `taskpool` built-in | Durable run queue. `WithConcurrency(MAX_CONCURRENT_RUNS)` is **per replica**, so capacity grows with replicas. Task ID = run ID, and the handler is `runner.Execute(runID)` |
| `reflection` | `taskpool` built-in | Post-run reflection, concurrency 1 per replica, so it never delays run completion |
| `image-builds` | `taskpool` built-in | Job image builds (§4.11), concurrency 1 per replica. Task ID = image ID |
| `run-cancel/<runId>` | `signal` built-in | A cancel request reaches the replica executing the run, whichever replica received the API call |
| `RunReconciler` | `cronjob` built-in | Every minute: runs in `provisioning`/`running`/`verifying` whose `heartbeat_at` is older than 60 s are failed as `interrupted`, their sandbox is destroyed, and the job actor is notified |
| `RetentionPrune` | `cronjob` built-in | Daily: prunes events and blobs older than the workspace's retention |
| `ImageGC` | `cronjob` built-in | Daily: deletes job images no longer referenced by the last 5 playbook versions (DB rows + registry) |
| `ModelCatalogRefresh` | `cronjob` built-in | Every `MODEL_CATALOG_REFRESH_INTERVAL` (12 h): downloads the models.dev catalog and syncs every provider's model list (§5.2) |
| rate limiters | `ratelimit` built-in | `/hooks/*` and the OIDC login endpoints |

**Invariants**
- **Never re-execute.** A task can be redelivered after a crash, so the run handler first checks the run's status and only proceeds from `queued`. A run found in `provisioning`/`running` is marked `failed (interrupted)` and its sandbox destroyed, **never re-executed**, because re-running could repeat side effects.
- **Heartbeat.** While a run executes, the runner updates `runs.heartbeat_at` every 15 s.
- **Test mode.** With `APP_ENV=test`, the maintenance cron jobs are not registered (same as Pocket ID).

**The one deliberate exception:** the **sandbox reaper** and the **local image-cache cleanup** run as a ticker on *every* replica, not as a Francis cron job. What they clean is host-local (each replica may talk to its own Docker daemon), and a singleton cron job would only ever see one host. Both are idempotent, and both decide from the database: destroy sandboxes whose run is terminal or past its TTL, and remove images `ImageGC` has deleted.

### 3.4 High availability

HA is built in, and enabling it is a deployment choice (Postgres, `HA_ENABLED=true`, a shared `FILE_BACKEND`, more replicas), not a code change. These rules keep it that way:

1. **No durable state in process memory.** Everything another replica might need lives in the database or in `FileStorage`:
   - Sessions and the OIDC login state (state, nonce, PKCE verifier) are signed, short-lived cookies.
   - Broker tokens are stored hashed on the run row, not in a map.
   - Rate limits, schedules, queues, timers and cancellation are Francis actors, alarms, taskpools and signals.
   - Settings are read from the database (or through an actor, as Pocket ID does with its app config), never cached per replica without invalidation.
2. **All async work goes through Francis.** Job triggers, runs, reflection, image builds and maintenance run as actors, taskpool tasks or cron jobs. A bare goroutine is only allowed within the lifetime of a request or a task, never for work that must survive a crash or happen exactly once. The only exception is the per-replica cleanup of host-local resources (§3.3).
3. **Live execution is host-local, results are not.**
   - A run's agent loop and its attached processes (stdio MCP servers) live on the replica that took the task, recorded in `runs.host_id`.
   - The adapter points `UMP_BROKER_URL` at that replica (§4.5).
   - Everything durable (status, events, state, outputs, blobs) is written to the database or `FileStorage` as it happens.
   - If the replica dies, the heartbeat goes stale and the `RunReconciler` fails the run as `interrupted`.
4. **Cross-replica live updates** use the event bus: in-process with SQLite (one replica by definition), and Postgres `LISTEN/NOTIFY` with Postgres. The bus only carries notifications and live token deltas. SSE always replays persisted events from the database by `seq`, so a client connected to any replica sees the whole timeline.
5. **Blobs** go through `storage.FileStorage`, copied from Pocket ID. That covers full tool outputs, artifacts and build logs. Backends: `filesystem` (single node or a shared volume), `s3`, or `database`.
6. **Replica-safe startup:** on Postgres, goose takes an advisory lock, so replicas can start together. There is no "mark crashed runs" step at startup: the `RunReconciler` (§3.3) handles interrupted runs.

### 3.5 SaaS readiness

v1 behaves as a single-tenant app, but the data model and code paths are tenant-scoped, so a hosted version is additive work rather than a rewrite.

- **Workspaces:** there is a `workspaces` table. Every tenant-owned table has an indexed `workspace_id` column, and every query filters on it. Child tables that are only ever reached through a scoped parent (`job_state`, `playbook_versions`, `run_events`, `images`, …) inherit the scope; their services always load the scoped parent first. The default workspace has the fixed ID `00000000-0000-7000-8000-000000000001`, which every replica creates idempotently at start (with `workspaces.enabled` on, only on an instance without any workspace). A fixed ID means no replica can ever cache a stale one.
- **Members and roles:** `workspace_members` holds an owner (at most one per workspace, enforced by a partial unique index), admins and members. With `workspaces.enabled` off (the default) everyone who signs in joins the default workspace, the first as owner; with it on, sign-in accepts email invites and invite links, and a user without any workspace gets a personal one. Invites live in `workspace_invites`, as a hashed single-use link token or a lowercased email address matched against verified addresses only. Instance admins come from per-provider admin lists, act as owners everywhere, and can deactivate users (`users.disabled_at`). The session cookie names the workspace, and the auth middleware resolves the role from the database on every request; API tokens act with their creator's membership role. Route access rules are declared with `httpserver.Restrict`.
- **Explicit scoping in code:** service methods take `workspaceID` as an explicit parameter (as Pocket ID passes `userID`). It is resolved as follows:
  - from the session or API token, by the auth middleware
  - from the run, for broker calls
  - from the job, for webhooks

  A unit test parses every module's `queries.sql` and fails if a query on a tenant-owned table doesn't reference `workspace_id`.
- **Users:** each login creates or updates a `users` row (issuer, subject, email, name), used for memberships, "triggered by" and "edited by".
- **Workspace-scoped from the start:** providers, models, secrets, MCP servers, settings, spend caps and retention. Env config only holds infrastructure settings and the defaults for new workspaces.
- **Host-side egress guard:** Umpteenth itself calls HTTP MCP servers and model base URLs, and later outbound webhooks. A guard blocks private and link-local targets when `ALLOW_PRIVATE_NETWORK_TARGETS=false`. It defaults to `true` for self-hosting (local Ollama, LAN MCP servers) and must be `false` for SaaS.
- **What SaaS adds later:**
  - self-service sign-up without an identity provider
  - billing, based on the per-run costs that are already recorded
  - per-workspace concurrency limits (a `workspace/<id>` actor gating submissions)
  - per-workspace encryption keys (secrets already store a `key_id`)
  - a sandboxed image builder
  - the Kubernetes or microVM adapter with gVisor/Kata for hostile-tenant isolation

  The Docker-socket adapter is for self-hosting only.

---

## 4. Sandbox (generic adapter interface)

The core only talks to `sandbox.Adapter`, and Docker-specific details are confined to `internal/sandbox/docker`. This follows Pocket ID's `storage.FileStorage` pattern: an interface with a `Type()`, one package per implementation, and a `switch` in bootstrap that picks one based on config.

### 4.1 Interface

```go
package sandbox

const TypeDocker = "docker" // value of SANDBOX_ADAPTER; later e.g. "kubernetes", "firecracker"

// Adapter provisions and manages sandboxes on one isolation backend
type Adapter interface {
    Type() string
    Check(ctx context.Context) (Info, error)                // backend reachable, arch, isolation, capabilities
    Create(ctx context.Context, spec Spec) (Sandbox, error) // returns once the sandbox accepts Exec
    Get(ctx context.Context, id string) (Sandbox, error)    // reattach after a restart; ErrNotFound if gone
    List(ctx context.Context) ([]Summary, error)            // sandboxes owned by THIS instance (reaper input)
    Destroy(ctx context.Context, id string) error           // force-remove sandbox + everything created for it; idempotent
    Close() error
}

// ImageBuilder is optionally implemented by adapters that can build job images for their own backend
// Adapters without it (e.g. Kubernetes before an in-cluster builder exists) report Dockerfiles as unsupported
type ImageBuilder interface {
    BuildImage(ctx context.Context, spec BuildSpec) (Image, error) // Dockerfile, tag, limits; logs streamed to spec.Logs
    HasImage(ctx context.Context, ref string) (bool, error)        // present locally or pullable from the registry
    RemoveImage(ctx context.Context, ref string) error
}

type Sandbox interface {
    ID() string
    Exec(ctx context.Context, req ExecRequest) (ExecResult, error)             // ctx cancel / Timeout kills the process tree
    Attach(ctx context.Context, req ExecRequest) (Process, error)              // long-lived process with stdin/stdout (stdio MCP)
    PutFiles(ctx context.Context, files []File) error                          // create/overwrite, with mode and owner
    ReadFile(ctx context.Context, path string, max int64) ([]byte, error)
    Archive(ctx context.Context, path string, max int64) (io.ReadCloser, error) // tar of a directory (artifact collection)
}

type Spec struct {
    RunID, JobID, WorkspaceID string
    Image     string            // OCI reference, resolved by the core (§4.3); VM-based adapters convert it to a rootfs
    Resources Resources         // CPUs, MemoryMB (0 = adapter default)
    Network   NetworkPolicy     // NetworkNone | NetworkInternet (later: allow-list)
    Env       map[string]string // job-declared secrets only
    AgentUser User              // UserAgent (default) | UserRoot (per-job opt-in)
    Broker    BrokerAccess      // per-run token + executing host; the adapter makes it reachable and sets UMP_BROKER_URL + UMP_TOKEN
    TTL       time.Duration     // hard upper bound, enforced by the backend where possible
}

// User is a role, not a uid; adapters map it (agent=1000, mcp=1001, root=0), so images need no passwd entries
type User string // "agent" | "mcp" | "root"

type ExecRequest struct {
    Cmd            []string
    WorkDir        string
    Env            map[string]string
    User           User
    Stdin          io.Reader
    Stdout, Stderr io.Writer // streamed live; truncation and log capture are done by the caller
    Timeout        time.Duration
}
type ExecResult struct { ExitCode int; Duration time.Duration; TimedOut, OOMKilled bool }

type Process interface { io.ReadWriteCloser; Stderr() io.Reader; Wait() (ExecResult, error) }

type File struct { Path string; Mode fs.FileMode; Owner User; Content io.Reader; Size int64 }

type Info struct {
    Adapter, Version string
    Arch      string    // amd64 | arm64 → which embedded ump binary to inject
    Isolation Isolation // container | gvisor | microvm (shown in the UI and on each run)
    Caps      Capabilities
}

type Capabilities struct {
    Networks      []NetworkPolicy // policies this adapter can enforce
    SeparateUsers bool            // exec as distinct uids (keeps stdio MCP env away from the agent)
    Limits        LimitSet        // which of CPU / memory / PIDs / disk are enforced
}

var ErrNotFound, ErrUnsupported, ErrSandboxGone error // adapters translate backend errors into these
```

**Capabilities are checked twice:**
- **When a job is saved**, as UI warnings, e.g. *"`network: none` is not supported by the active sandbox adapter"* or *"Dockerfiles need an adapter that can build images"*.
- **At run start.** The run fails fast with a clear reason instead of silently running with weaker isolation.

### 4.2 Adapter contract

Every adapter guarantees the following, and the conformance suite (§4.6) checks each point:
1. **Isolation between runs:** two sandboxes have no network path to each other and do not share a filesystem.
2. **Network policy:** `NetworkNone` means no route to the internet, but the broker is still reachable. `NetworkInternet` means outbound internet plus the broker.
3. **Layout:** `/workspace` exists, is owned by the agent user, and is the default working directory. `/ump` is writable by the agent user.
4. **Users:** with `SeparateUsers`, `agent` and `mcp` get distinct uids, and the agent cannot read the `mcp` user's process environment.
5. **Cancellation:** cancelling an `Exec` context, or hitting `Timeout`, terminates the whole process tree within 2 s without destroying the sandbox.
6. **Cleanup:** `Destroy` is idempotent and removes every resource the adapter created for the run (networks, volumes, pods, policies, tap devices, …).
7. **Ownership:** `List` returns only sandboxes carrying this instance's ID (Pocket ID's `instanceid` pattern), so two Umpteenth instances on one backend never reap each other's sandboxes.
8. **Limits:** every limit in `Resources` is either enforced or reported as unsupported in `Caps.Limits`.

### 4.3 What the core does (once, for every adapter)

1. **Resolve the image.** In order of preference:
   - the ready job image for the playbook's Dockerfile (§4.11)
   - the job's base image
   - `SANDBOX_IMAGE`
2. **Create** the sandbox and store `sandbox_id` and `host_id` on the run, so crash recovery and the reaper can find it.
3. **Inject files** with `PutFiles`:
   - `/usr/local/bin/ump`: the embedded CLI for `Info.Arch`
   - `/ump/toolkit/*`: playbook scripts
   - `/ump/input.json`: trigger payload and run input
   - `/ump/PLAYBOOK.md`: human-readable playbook, for the agent to `cat` if needed
4. **Setup**: run the playbook `setup` script if present. It is meant for cheap per-run steps; installs belong in the Dockerfile.
5. **Execute** in the chosen mode (§6, §10).
6. **Collect** `/ump/outputs/` via `Archive` (cap 50 MB) into `FileStorage` under `runs/<id>/artifacts/`.
7. **Destroy**: always deferred. The per-replica reaper (§3.3) is the safety net.

The following all live in the core, so adapters stay small:
- output truncation and log capture
- capability checks
- `sandbox.*` events and spans
- the reaper policy

### 4.4 Exec semantics
- Each `bash` tool call is `bash -lc '<cmd>'` in `/workspace`. **Files persist between calls; shell variables don't.** The model is told this explicitly.
- Per-command timeout: 120 s by default, max 600 s. Cancelling the run kills the running exec and destroys the sandbox.
- Output is streamed live to the UI. The LLM gets at most ~30k chars (first 8k + last 22k). The **full output is written into the sandbox** at `/ump/logs/<call-id>.txt`, and the truncation notice tells the agent it can `grep` it.

### 4.5 Broker reachability (adapter responsibility)

The broker is a plain HTTP listener (`BROKER_PORT`, default 8081), authenticated by the per-run `UMP_TOKEN`. How a sandbox reaches it depends on the backend, so **each adapter owns that path** and injects `UMP_BROKER_URL`. The core never assumes a particular network topology.

In HA, the URL must target **the replica executing the run**, because that is where the run's live state is: the agent loop and the stdio MCP processes (§3.4). Durable broker state, such as job state, outputs and the token hash, is in the database either way.

| Adapter | Path to the broker |
|---|---|
| Docker (v1) | Per-run internal network, where the executing replica is reachable as `umpteenth` (§4.7.3) |
| Kubernetes (later) | The executing replica's pod IP. A per-run `NetworkPolicy` allows egress to the Umpteenth pods only (plus the internet for `NetworkInternet`) and denies pod-to-pod traffic between sandboxes |
| MicroVM (later) | A per-VM tap or vsock device; the broker binds on the host side of it |

### 4.6 Adding an adapter & the conformance suite

Adding an adapter:
1. Create `internal/sandbox/<name>/` with `New(ctx, Config) (sandbox.Adapter, error)`, and optionally implement `ImageBuilder`.
2. Add a `Type…` constant and a `case` in `bootstrap.initSandboxAdapter`.
3. Add adapter-specific env vars `SANDBOX_<NAME>_*` to `common.EnvConfig`.
4. Pass `sandboxtest` in CI.
5. Document the adapter's security defaults, broker path and image story.

**`internal/sandbox/sandboxtest`** is a shared suite: `sandboxtest.Run(t, newAdapter, opts)`. It runs every contract point from §4.2 against a real backend (`-tags integration`), including:
- exec, env, workdir and streaming
- timeouts that kill a process tree
- OOM reporting
- file modes and owners
- `ReadFile` and `Archive` limits
- an `Attach` round-trip
- `/proc` environment isolation between users
- `NetworkNone`: no internet, but a stub broker is reachable
- sandbox-to-sandbox isolation
- `Get` after re-open
- `List` ownership and idempotent `Destroy`
- `ImageBuilder` build, `HasImage` and remove, when implemented

**`internal/sandbox/fake`** is an in-memory adapter for unit tests of the agent loop and runner.

### 4.7 Docker / Podman adapter (v1)

#### 4.7.1 Mapping
- **Create:** a container from the resolved image. Labels: `umpteenth.instance=<id>`, `umpteenth.run=<id>`, `umpteenth.job=<id>`, `umpteenth.host=<replica>`. Workdir `/workspace`.
  - The adapter copies the embedded `ump` binary for the engine's architecture into the container and runs `ump init` as PID 1. It reaps zombies like `tini -- sleep infinity` would, and also enforces `Spec.TTL`. The core never injects anything, so any OCI image works, even one without tini or a shell.
- **PutFiles / Archive:** tar via `CopyToContainer` / `CopyFromContainer`. This works whether Umpteenth runs on the host or in a container, so there are no bind-mount path problems.
- **Exec cancellation:** the Engine API has no way to kill an exec. So commands are started through the injected `ump exec` shim, which starts the command in its own process group and records the PID. Cancelling sends a second exec with `kill -TERM -<pgid>`, then `KILL` after 1.5 s.
- **List / Destroy:** by label. `Destroy` also removes the per-run network.
- **Crash recovery:** a replica that crashes mid-provisioning can leave a run network without a sandbox, and Docker re-attaches the replica's own container to it on restart. `Prepare` therefore prunes every run network of this replica created before the process started, which also detaches the container. `Close` removes the broker relay in binary mode, and the next start recreates it.
- **ImageBuilder:**
  - Builds use the Engine API with BuildKit. Tags look like `umpteenth/job-<jobId>:<dockerfileHash>`.
  - When `SANDBOX_REGISTRY` is set, images are pushed there and other replicas pull them.
  - Without a registry, a replica that lacks the image rebuilds it from the same Dockerfile and pinned base digest (§4.11).
- **Podman:** same adapter, via Podman's Docker-compatible socket (`DOCKER_HOST`).

#### 4.7.2 Security defaults
| Setting | Default |
|---|---|
| User | `agent` (uid 1000). `root` is an opt-in per job (for `apt` at runtime; prefer the Dockerfile) |
| Capabilities | `--cap-drop=ALL` (non-root), Docker defaults when root is opted in |
| `no-new-privileges` | on |
| Limits | 1 CPU, 1 GiB RAM, 256 PIDs, 15 min wall clock (all per job) |
| Runtime | `runc`. `runsc` (gVisor) if `SANDBOX_DOCKER_RUNTIME=runsc`, which `Check` verifies via `docker info` (then `Info.Isolation = gvisor`). `Prepare` also starts a test sandbox under any custom runtime and copies a file in and out; a failure refuses runs and shows the fix. runsc needs `--overlay2=none --file-access=shared`, since the adapter moves files through the Docker API |
| Rootfs | writable (the container is disposable anyway, and it allows `pip`/`npm` installs) |
| Docker socket exposure | Recommended in the docs: **rootless Podman**, or a socket proxy limited to containers/exec/images/networks/build |

#### 4.7.3 Networking
- **Per-run internal network:** `ump-run-<id>` (`--internal`, so no internet) means **sandboxes can never reach each other**. The sandbox reaches the broker at `http://umpteenth:8081` in both deployment modes:
  - **Container mode** (recommended): the adapter connects the *executing replica's* container to the per-run network with the alias `umpteenth`.
  - **Binary mode** (Umpteenth runs directly on the host, e.g. in development): an `--internal` network has no reliable route to the host, so the adapter runs one long-lived relay container, `umpteenth-relay`.
    - The relay uses the sandbox image with an injected `ump relay`.
    - It sits on the default bridge and forwards to `host-gateway:8081`.
    - It joins each per-run network with the alias `umpteenth`.
    - It only forwards the broker port, so it is not a router between runs.
- **Egress proxy** (replaced the host firewall on 2026-09-27): `internet` and `allowlist` sandboxes stay on their internal network and reach the outside only through a proxy on the broker listener, so the host firewall is never touched and every engine behaves the same.
  - It speaks HTTP (`CONNECT` and absolute URLs) and SOCKS5 (`CONNECT`) on the broker port and knows the sandbox by its broker token as the proxy password. The adapter sets `HTTP(S)_PROXY`, `ALL_PROXY=socks5h://…`, `NO_PROXY` and `NODE_USE_ENV_PROXY`, and an SSH `ProxyCommand` through `ump connect`.
  - Every dialed address is checked like the host-side egress guard (§3.5): private, shared, loopback, link-local, metadata and special ranges are refused, and "allow private network" only keeps loopback, link-local and metadata refused. `network.blocked_targets` is refused for every job and for the guard, and so is every container address on the engine apart from network gateways, since the proxy dials from Umpteenth's process, which reaches them all.
  - **`network: internet`** (default) passes any name or IP literal to that check. **`network: allowlist`** passes only the job's allowed domains (`example.com`, `*.example.com`) and never an IP literal.
  - The first connection per host and every refusal land on the run's timeline.
- **`network: unrestricted`**: a plain shared bridge `ump-unrestricted` with `enable_icc=false` (a per-run isolated bridge on Podman), without proxy or checks, for tools that ignore proxy settings. `sandbox.allow_unrestricted_network` (default `true`) decides whether the adapter offers it.
- **`network: none`**: internal network only. It can reach the broker (so MCP via `ump` still works) but not the internet.
- **Only the broker is reachable:** the Umpteenth container joins every per-run network, so its other listeners (the API, the actor peer port) refuse connections that arrive on a sandbox network.

### 4.8 Future adapters (design targets, not v1)

| Adapter | Isolation | Image handling | Files / exec | Notes |
|---|---|---|---|---|
| **Kubernetes** | Pod. Optionally a `RuntimeClass` for gVisor/Kata | OCI, native. Job images come from an in-cluster rootless BuildKit that pushes to `SANDBOX_REGISTRY` | Exec and tar through the API server | One Pod per run. `List` uses labels. `activeDeadlineSeconds` = `Spec.TTL`. Per-run `NetworkPolicy`. Umpteenth runs in the cluster with a namespaced ServiceAccount. **The target adapter for SaaS** |
| **Firecracker / Kata / Apple `container` / Lima** | microVM | OCI → rootfs (cached per image digest) | A guest agent over vsock. The `ump` binary can double as the guest agent (`ump agentd`) | Firecracker needs Linux + KVM. Apple `container` is macOS-only |

The interface deliberately avoids Docker-isms: no labels or container IDs as concepts, no tar in the write path, and users are roles. That keeps these adapters feasible.

### 4.9 Default sandbox image `umpteenth-sandbox`
- **Contents:** based on `debian:trixie-slim`, with `bash coreutils curl ca-certificates git jq ripgrep tini python3 uv nodejs npm`, plus users `agent` (1000) and `mcp` (1001). About 250 MB.
- **Why Node, Python and uv:** so `npx`/`uvx` stdio MCP servers work.
- **Base for job images:** it is the recommended `FROM` for job Dockerfiles.
- **No `USER`:** the image sets none, because the adapter picks the uid per exec.
- **Any OCI image works** per job: the `ump` CLI is injected at runtime, and users are mapped by uid.

### 4.10 `ump` CLI (inside the sandbox) and broker (host)

```
ump mcp tools [server]                  # list tools + JSON schemas
ump mcp call <server> <tool> [json|-]   # call an MCP tool through the host, print JSON result
ump llm [--model utility|agent] [--schema schema.json] [--system "..."] < prompt
                                        # one bounded LLM call, no tools; prints text or JSON
ump state get <key> | set <key> [value|-] | list   # per-job persistent state
ump output set <key> [value|-]          # structured run output (shown in UI, exposed via API)
ump step "<name>"                       # progress marker → timeline + resume point for fallback
ump fail "<reason>"                     # explicit failure (non-zero exit + reason in UI)
```
Internal subcommands, not for scripts: `ump exec` (the process-group shim, §4.7.1) and `ump relay` (§4.7.3).

The broker authenticates with `UMP_TOKEN`, a random per-run token injected as an env var.
- Its hash is stored on the run, and it is invalidated when the run ends.
- It only exposes the MCP servers, tools, models and state of **that job**.
- Every broker call is recorded as a run event, so scripted runs get timelines as rich as agent runs.

### 4.11 Job images (per-job Dockerfile)

A job can bring its own environment, so tools are installed once instead of on every run.

- **Where it lives:** in the playbook (`dockerfile` field). Every change is a version with a diff and rollback, and rolling back also restores the matching environment. `null` means the job uses its base image (`jobs.image`) or `SANDBOX_IMAGE`.
- **Who writes it:**
  - You, in the job's **Environment** tab (CodeMirror editor + build log).
  - The compile step, which proposes one when the instruction implies tools (e.g. "transcode with ffmpeg").
  - Reflection, through a `set_dockerfile` op. It is told to move `setup` steps that keep costing time into the image (§9.2).
- **Build:**
  - A new Dockerfile hash creates an `images` row and submits it to the `image-builds` taskpool.
  - The build context is the Dockerfile only, with no host files.
  - Limits: 15 min build time and 5 GB image size (configurable).
  - `FROM` is resolved to a digest at the first build and pinned, so every replica builds the same image. "Rebuild" re-resolves it to pick up base-image updates.
- **Runs** use the newest ready image for their playbook's Dockerfile hash.
  - **Still building:** the run waits in `provisioning` (span `sandbox.image_wait`).
  - **Build failed:** the run fails with "environment build failed" and a link to the log.
  - **No silent fallback** to an image built from an *older* Dockerfile, because toolkit scripts may rely on the new tools.
- **Across replicas:** images are pushed to `SANDBOX_REGISTRY` when it is set. Otherwise each replica builds on first use (§4.7.1).
- **Security:** build steps run under the builder's isolation (the classic builder's runc, default capabilities, no gVisor), which is weaker than the sandbox. They run on a per-build internal network with the broker attached and reach the internet through the egress proxy (Docker's predefined proxy build args), so they reach the internet but not private networks, the host or metadata, like an internet sandbox.
  - That is acceptable for self-hosting, where Dockerfiles come from the admin or from reflection under the self-improve toggle.
  - SaaS needs a sandboxed rootless builder.
  - Build secrets (private package registries) come later via `RUN --mount=type=secret` from job secrets.
- **Adapters without `ImageBuilder`** show a capability warning, and jobs with a Dockerfile can't run on them.

---

## 5. LLM layer (provider-agnostic)

### 5.1 Interface

```go
package llm

type Provider interface {
    Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
    Caps(model string) Caps
}

type Request struct {
    Model        string
    System       []Block        // stable → volatile. Cache hints attached by the adapter
    Messages     []Message      // append-only history
    Tools        []ToolDef      // name, description, JSON Schema. Sorted, deterministic
    MaxTokens    int
    Effort       Effort         // low | medium | high → mapped per provider
    OutputSchema *jsonschema.Schema // structured output (compile, reflection, verify)
}

type Message struct { Role Role; Parts []Part }
// Part = Text | ToolCall{ID, Name, Args json.RawMessage} | ToolResult{CallID, Content, IsError}
//      | Reasoning{Provider string; Opaque json.RawMessage}  // echoed back unchanged

type Usage struct { Input, Output, CacheRead, CacheWrite, Reasoning int }
type Response struct { Message Message; Stop StopReason; Usage Usage; Latency time.Duration }

type Caps struct { Tools, ParallelTools, Reasoning, JSONSchema, PromptCache, Vision bool; Context int }
```

### 5.2 Adapters
| Adapter | SDK | Covers | Notes |
|---|---|---|---|
| `anthropic` | `github.com/anthropics/anthropic-sdk-go` (native) | Claude models | Adaptive thinking; `output_config.effort`; `cache_control` on system+tools plus automatic caching of the conversation tail; thinking blocks kept opaque and echoed back unchanged; structured outputs via `output_config.format`; `refusal` stop reason handled |
| `openai` | `github.com/openai/openai-go` (Chat Completions) | OpenAI, Ollama, OpenRouter, vLLM, LM Studio, Groq, Mistral, Gemini's OpenAI endpoint | `base_url` per provider; `response_format: json_schema` when supported; usage normalized (`prompt_tokens` includes cached → split into Input/CacheRead) |
| `fake` | – | Tests only (`e2etest`/`unit`) | Replays scripted responses queued via `POST /api/test/llm-script` or set directly in unit tests |

**Structured output fallback**: if a model lacks JSON-schema output, the adapter defines a `submit` tool with that schema and tells the model to call it. This works on any tool-capable model.

**Minimum model requirement:** tool calling. Models without it can only be used as the `utility` model for `ump llm` text calls.

**Module split:**
- The `providers` module owns the DB side (providers, models, prices, per workspace) and builds `llm.Provider` instances from it.
- The `llm` package holds the interface, the adapters and the model catalog.
- This mirrors the sandbox split, with one difference: LLM adapters are chosen **per provider row**, whereas the sandbox adapter is chosen **per instance**.

**Model catalog and sync:**
- The catalog is built from [models.dev](https://models.dev) (MIT): the Anthropic and OpenAI models an agent can run on, plus the traits of open model families across every host, keyed by a normalized name so `qwen3:32b` and `Qwen/Qwen3-32B` match.
- `go generate ./internal/llm` snapshots it into `catalog.json`, which is embedded, so an offline instance still has a catalog.
- The `ModelCatalogRefresh` cron job downloads models.dev every `MODEL_CATALOG_REFRESH_INTERVAL` (12 h, `0` turns it off), stores the result in `model_catalog`, and syncs every provider. Other replicas load the stored catalog within a minute.
- **Catalog providers** (Anthropic, and OpenAI without a base URL or with `api.openai.com`) list the catalog, and their models follow its metadata (`follow_catalog`) until an admin edits them.
- **Server providers** (every other OpenAI-compatible API) list `GET /models`. The extra fields servers send (`max_model_len`, `context_length`, `context_window`, `meta.n_ctx_train`, OpenRouter's `pricing` and `supported_parameters`) come first, the models.dev family fills the gaps, and the metadata is never refreshed after discovery.
- A sync never deletes: models the source stops listing get `unlisted_at` and keep their settings. Admins turn models off (`enabled`) instead of deleting them, since a sync would add a deleted model back.
- Only enabled, listed models can be picked. A job whose model became unavailable runs on the workspace's agent model, and a model a workspace default points at can't be turned off.
- The stored capabilities of a model, not the adapter's own lookup, decide how it is called (native JSON schema vs the submit tool).

### 5.3 Model roles
| Role | Used for | Suggested default |
|---|---|---|
| `agent` (per job) | Explore/Assisted runs | `claude-opus-5-5` |
| `reflection` (per workspace, per-job override) | Playbook updates, script extraction, graduation | same as the job's agent model |
| `utility` (per workspace) | Compile step, verification, `ump llm` default | `claude-haiku-4-5` (or a local Ollama model) |

### 5.4 Cost accounting
- **Prices:** providers and models live in the DB, per workspace, with prices in **micro-USD per 1M tokens** (input, output, cache read, cache write). They come from the model catalog (§5.2) and are editable, and local models cost 0 unless their server reports a price.
- **Formula:** `cost = in·p_in + out·p_out + cache_read·p_cr + cache_write·p_cw`, stored as integer micro-USD.
- **Recording:** per LLM call, rolled up per run and per workspace. Reflection and verification costs are tracked separately, so charts can split "run cost" from "learning cost".
- **Guards:** per-run max cost (default $2), per-run `max_turns` (60), and a per-workspace daily spend cap. Hitting any of them ends the run as `failed` with a clear reason.

### 5.5 Prompt layout (cache-friendly on every provider)
Everything before the first user message is **byte-stable for a given job + playbook version**: no timestamps, sorted tools, deterministic rendering. Consecutive turns and back-to-back runs then hit the provider's prefix cache.
```
[system]  1. Umpteenth base instructions                  (static)
          2. Job instruction + compiled success criteria  (changes on job edit)
          3. Playbook render: learnings + toolkit index   (changes on playbook version)
[tools]   builtin tools + toolkit tools + allowed MCP tools (sorted)
[user]    run context: trigger, time, input summary, "additional instructions for this run"
```

---

## 6. Agent runtime (Explore / Assisted)

### 6.1 Loop
```
for turn := 1; ; turn++ {
    check budgets (turns, cost, wall clock) → stop if exceeded
    resp := provider.Stream(req, emitDelta)            // live tokens → event bus → SSE (not persisted)
    record llm.call event (usage, cost, latency)
    if resp has no tool calls → nudge once to call `finish`; then fail
    run tool calls: read-only tools in parallel, others in order
    append all results in ONE message                  // keeps parallel tool use working
    if `finish` was called → break
}
```
History is append-only. For runs that approach the context limit:
1. Output truncation (§4.4) comes first.
2. When the next prompt would pass 70% of the model's context window, or the provider rejects a prompt as too long, a separate call without tools summarizes the conversation into a handover (goal, progress, exact facts, dead ends, next steps). The run continues from its first message plus the handover, and the last exchange when it takes at most 20% of the window. The summary call is paid for like any turn and shown on the timeline. Two compactions never happen on consecutive turns.
3. Provider-native compaction is not used, so all providers behave the same.

### 6.2 Built-in tools (our own JSON schemas, so they work on any provider)
| Tool | Purpose |
|---|---|
| `bash(command, timeout_sec?)` | Run a command in the sandbox |
| `read_file(path, offset?, limit?)` | Read a text file (line-numbered) |
| `write_file(path, content)` | Create or overwrite a file |
| `edit_file(path, old, new)` | Exact string replace; fails if `old` isn't unique |
| `state_get(key)` / `state_set(key, value)` | Per-job persistent state |
| `remember(note)` | Flag something surprising (an edge case, a workaround). Goes straight into reflection |
| `finish(status, summary, outputs?)` | **Required** end of run: `success`/`failure`, a markdown summary, structured outputs |
| `toolkit__<name>(args)` | One tool per playbook script (Assisted mode) |
| `<server>__<tool>__<id>(…)` | Allowed MCP tools, with a stable identity suffix |

### 6.3 Base-instruction essentials (what the agent is told)
- You are in a disposable Linux sandbox. `/workspace` is your working directory and nothing outside it persists.
- Prefer toolkit tools. They were proven in earlier runs.
- When you write deterministic multi-step logic, **write it as a script under `/ump/candidates/`** with a `ump:` header (§10.2) and *run that script*. Tested scripts are what reflection promotes.
- If you had to install something, say so with `remember`, so reflection can move it into the job's Dockerfile.
- Use `remember` for anything that surprised you.
- Always end with `finish`.

---

## 7. Jobs

### 7.1 Defining a job (language only)
The user writes one text box. Example:
> Every weekday at 8:00 Berlin time, look at open pull requests in `acme/api` that have had no activity for 3+ days. Post a short summary to the #eng Slack channel, grouped by author. If there are none, don't post.

**Compile step** (`utility` model, structured output) → **Spec**:
```json
{
  "title": "Stale PR digest for acme/api",
  "goal": "Post a grouped summary of stale PRs to #eng on weekdays",
  "schedule": { "cron": "0 8 * * 1-5", "timezone": "Europe/Berlin", "human": "Weekdays at 08:00" },
  "success_criteria": [
    "All open PRs in acme/api with no activity for ≥3 days are considered",
    "If any exist, exactly one message is posted to #eng, grouped by author",
    "If none exist, nothing is posted"
  ],
  "inputs": [],
  "outputs": [{ "name": "stale_count", "type": "integer" }],
  "mcp": [{ "server": "github", "why": "list PRs" }, { "server": "slack", "why": "post message" }],
  "network": "internet",
  "dockerfile": null,
  "side_effects": ["posts to Slack"],
  "warnings": []
}
```
- **Editable spec:** the UI shows the spec as editable cards. Changed fields go into `spec_overrides` and survive recompiles.
- **Proposed Dockerfile:** a non-null `dockerfile` becomes the first playbook version's Dockerfile after you confirm it.
- **Warnings** come from the spec and the active sandbox adapter's capabilities. For example:
  - *"mentions Slack, but no Slack MCP server is configured."*
  - *"`network: none` is not supported by the active sandbox adapter."*
- **Instruction edits** trigger a recompile and a playbook note: *"instruction changed at v12"*. Reflection re-checks learnings against the new instruction.

### 7.2 Triggers
All triggers go through the job's actor (§3.3), whichever replica received them. The actor applies the concurrency policy and submits to the `runs` taskpool.

| Trigger | Details |
|---|---|
| Manual | "Run now", with optional extra text for this run |
| Schedule | Cron + timezone. The job actor keeps a durable Francis alarm for the next occurrence, so it survives restarts and failovers and never double-fires |
| Webhook | `POST /hooks/{jobId}` with `Authorization: Bearer <job webhook token>`, rate-limited. The body becomes `/ump/input.json` |
| API | `POST /api/jobs/{id}/runs` with an API token |

**Concurrency policy per job:**
- `skip` (default): skip if a run is active, and record a `skipped` run.
- `queue`: hold the trigger until the active run finishes.
- `parallel`: start right away.

The global limit is `MAX_CONCURRENT_RUNS` per replica (default 3), which is the taskpool's concurrency.

---

## 8. MCP

- **Registry** (per workspace, UI-managed): name, description, transport, and either:
  - **HTTP** (Streamable HTTP):
    - `url` + headers, where header values can reference secrets (`{{secret:GITHUB_TOKEN}}`).
    - **The connection is made from the host**, subject to the egress guard (§3.5), so credentials never enter the sandbox.
  - **stdio**: `command`, `args`, `env`.
    - **It is launched inside the run's sandbox** as user `mcp` via `Sandbox.Attach`. Arbitrary `npx`/`uvx` packages never run on the host.
    - Because it runs as a different uid, the agent (user `agent`) can't read its environment through `/proc`.
    - If the active adapter lacks `SeparateUsers`, stdio servers with secret env vars are refused with a clear warning.
- **Client**: official `github.com/modelcontextprotocol/go-sdk`. stdio-in-sandbox uses a small custom transport over the `Process` stream.
- **Per job**: attached servers plus an optional tool allow-list. The compile step pre-selects servers and tools. The UI's "Test" button connects and lists tools, cached in `tools_cache`.
- **Exposure**: MCP tools are exposed to the LLM with a readable `<server>__<tool>` prefix and a stable identity suffix that prevents provider-name collisions, and to scripts through `ump mcp call` via the broker. Both paths go through the same host-side MCP manager, which records every call.
- **Tool annotations** (`readOnlyHint`, `destructiveHint`) are stored and used to (a) mark scripts as having side effects and (b) run read-only calls in parallel.
- **OAuth login** for HTTP servers, detected and performed the way Codex does it (decided 2026-09-26):
  - Detection on save and test: an `Authorization` header counts as a bearer token, a stored login as logged in; otherwise the endpoint's 401 challenge, the protected resource metadata (RFC 9728) and the authorization server metadata (RFC 8414, OIDC discovery) decide between "not logged in" and "no login". Adding a server that advertises a login opens the login right away, like `codex mcp add`.
  - Login is a browser redirect: dynamic client registration (or a client ID/secret set on the server), PKCE S256, the RFC 8707 `resource`, `offline_access` when offered, and one retry without scopes when the provider rejects the discovered ones. Each server has its own callback, `APP_URL/api/mcp-servers/{id}/oauth/callback`, and the started login waits encrypted in the database, bound to the user who started it, so any replica can finish it.
  - Tokens are encrypted in `mcp_servers`, used only from the host, refreshed 30 s before they expire and once more when a server rejects one early. A refresh reads the database first, since another replica may have rotated the refresh token already, and a rejected refresh token ends the login.
- **Later:** lazy tool loading ("tool search") for jobs with huge tool sets.

---

## 9. Self-improvement (the Playbook)

### 9.1 Playbook content (one JSON document per version)
```json
{
  "learnings": [
    { "id": "L3", "kind": "edge_case",
      "text": "GitHub search caps at 1000 results; filter with updated:<DATE instead of paging.",
      "when": "listing PRs", "sources": ["run_0192…"], "hits": 5, "status": "active" }
  ],
  "toolkit": [
    { "name": "list_stale_prs", "lang": "python",
      "description": "Print JSON list of open PRs with no activity for N days",
      "args": { "repo": "string", "days": "integer" }, "side_effects": false,
      "content": "…", "sources": ["run_0192…"], "stats": { "calls": 9, "failures": 0 } }
  ],
  "dockerfile": "FROM ghcr.io/stonith404/umpteenth-sandbox\nRUN uv pip install --system requests",
  "setup": null,
  "main": null,
  "verify": null
}
```
The rendered playbook in the prompt has a **size budget of ~3k tokens**. When it's over budget, reflection has to merge or retire entries. The Dockerfile is not rendered into the prompt; the agent just sees the tools it installed.

### 9.2 Reflection (post-run, async; never delays run completion)
- **Runs when** `self_improve` is on **and** at least one of these holds:
  - the mode was Explore or Assisted
  - the run failed
  - a scripted run fell back
  - the agent used `remember`

  A **successful scripted run skips reflection**, so it costs nothing.
- **Input:**
  - the instruction + spec, and the current playbook
  - a condensed transcript: tool calls with truncated output, errors, retries, `remember` notes, the `finish` result
  - the `/ump/candidates/*` scripts
  - setup and install timings, and stats vs previous runs
- **Output** (structured): a list of operations, each with a rationale:
  `add_learning · update_learning · retire_learning · upsert_script · delete_script · set_setup · set_dockerfile · propose_main · update_main · set_verify`
- **Environment rule:** when `setup` (or installs the agent did by hand) took more than 10 s in each of the last 3 runs, reflection should move that work into the Dockerfile with `set_dockerfile`. That is how a job gets **faster** as well as cheaper.
- **Applying:** the operations are validated (schema, size budget, script header parse, Dockerfile parse) and **applied as a new playbook version** with `author=reflection` and `source_run_id`. A changed Dockerfile triggers a build (§4.11). The run page shows the diff ("What this run taught the job").
- **Runs on** the `reflection` taskpool (§3.3).

### 9.3 Toggle and history (your requirement)
- **`self_improve = on`:** reflection runs automatically and its versions go live immediately.
- **`self_improve = off`:** the playbook is frozen and no reflection cost is spent.
  - **Runs are still fully recorded** (events, transcripts, outputs).
  - On any past run you can click **"Learn from this run"** to reflect once manually.
  - Turning improvement on later can offer to learn from the last N saved runs.
- **Every playbook change is a version**, whether it came from reflection, a manual edit, or a rollback. The history view shows diffs, the source run, and one-click rollback. Rollback creates a new version, so history is never rewritten.
- **Manual edits:** you can always edit the playbook by hand (learnings as text, scripts and the Dockerfile in a code editor).

### 9.4 Safety of learning
- **Learning poisoning:** tool outputs (web pages, MCP results) are untrusted and could smuggle instructions into "learnings" or the Dockerfile. Mitigations:
  - the reflection prompt treats transcript content as data
  - learnings containing URLs, credentials, or imperative instructions about *other* systems are flagged
  - Dockerfile ops that add new `FROM` registries or `curl | sh` patterns are flagged
  - provenance links back to the run
  - rollback, and the per-job toggle
- **Drift:** learnings carry `hits` counts. Reflection must justify additions against the existing list (update over add).

---

## 10. Graduation: LLM + script hybrid

### 10.1 Modes
| Mode | When | Who drives | Typical cost* |
|---|---|---|---|
| **Explore** | Playbook empty | LLM agent, all tools | $$$ |
| **Assisted** | Playbook has learnings/toolkit | LLM agent, toolkit scripts as first-class tools | $$ |
| **Scripted** | Playbook has `main` and the job isn't demoted | `main` runs with no agent loop. It may call `ump llm` for bounded judgment steps. The utility model verifies | ¢ |

\*Illustrative. The point is the curve, and the UI makes it visible.

Mode is **derived** from playbook + history. The only override is the per-job `graduate` switch (on by default): turned off, runs never go past Assisted and reflection neither proposes nor repairs `main`, for jobs that need the agent's judgment every run.

### 10.2 Script header convention (for candidates and toolkit)
```bash
#!/usr/bin/env bash
# ump:name        list_stale_prs
# ump:description Print JSON list of open PRs with no activity for N days
# ump:args        {"repo":"string","days":"integer"}
# ump:side-effects none        # none | external (posts, writes, sends)
```

### 10.3 Graduation criteria (automatic when `self_improve` is on)
Reflection may `propose_main` when **all** of these are true:
1. The last **3** runs (configurable) were successful Assisted runs.
2. In those runs the agent's actions were **mostly toolkit calls**: at most 2 ad-hoc `bash` calls per run beyond reading files.
3. The ordered sequence of toolkit/MCP calls was the same across those runs, apart from arguments.

`main` is composed from that sequence. Judgment steps (summarize, classify, pick top N) become `ump llm` calls with a JSON schema. `verify` is also set:
```json
{ "checks": ["exit_code == 0", "output.stale_count exists"],
  "llm": false }   // true only when the result can't be checked mechanically, e.g. a written summary
```
Deterministic checks run on every scripted run. The utility model only judges the stdout tail and outputs against the success criteria when reflection set `llm: true`, so most scripted runs cost nothing (decided 2026-09-26).

Graduation is automatic for every job with learning on, including jobs whose `main` has external side effects. Those rely on the side-effect log handed to the fallback agent (§10.4) instead of an approval click.

### 10.4 Scripted run flow
```
setup → ump step markers → run main (timeout) → deterministic checks → LLM verify (utility)
   ├─ pass → succeeded (no reflection)
   └─ fail → FALLBACK: start an Assisted agent in the SAME sandbox with:
             "main failed at step <last ump step>; stderr tail …; outputs so far …;
              these side effects already happened: …; finish the job, then fix the script."
             → reflection updates `main`  (self-healing)
             → 2 consecutive fallbacks ⇒ demote to Assisted until criteria are met again
```
**Side-effect safety:**
- The fallback agent is told exactly which steps already ran (from the `ump step` markers and recorded broker calls), so it doesn't post twice.
- Scripts with `side-effects: external` are **never** dry-run or shadow-run.
- Side-effect-free jobs shadow-run a new or changed `main` before reflection applies it (decided 2026-09-26): one run in a fresh sandbox with the input of the run reflection learned from. It must pass its checks and reproduce that run's declared outputs, except those `verify.varies` lists, or the change is rejected and the model gets its second attempt with main's output. The broker refuses MCP tools that aren't read-only and keeps `ump state set` in the shadow run, and jobs whose run changed their state are not shadowed, since main would start from the state the run left behind.
- A job that declares inputs must read them in `main` from `/ump/input.json`; a `main` that never reads it is rejected, since it would silently ignore every run that sets a different value.

### 10.5 Worked example (stale PR digest, illustrative numbers)
| Run | Mode | What happens | Turns | Cost |
|---|---|---|---|---|
| #1 | Explore | Agent discovers the GitHub MCP tools, hits the search cap, `pip install`s `requests`, writes `list_stale_prs.py`, posts to Slack | 38 | $0.61 |
| #2 | Assisted | Uses `list_stale_prs` plus learning L3. Reflection adds `format_digest.py` and moves `requests` into the Dockerfile | 9 | $0.14 |
| #3–#4 | Assisted | Same 3-step sequence both times, no installs at run time | 5 | $0.07 |
| #5 | **Scripted** | `main.sh`: `list_stale_prs.py \| ump llm --schema digest.json \| ump mcp call slack post_message -` + verify | 0 (+2 small LLM calls) | $0.01 |

---

## 11. Data model (SQLite or Postgres, via goose + sqlc)

- **IDs** are UUIDv7 (sortable).
- **Timestamps** are unix ms (`BIGINT`).
- **Money** is integer **micro-USD** (`BIGINT`, 1 = $0.000001). That avoids float rounding and avoids `REAL`, which is only 4 bytes in Postgres.
- **Booleans** are `BOOLEAN`, and **JSON** is stored as `TEXT` and handled in Go.

**Engines**
- **SQLite** (`modernc.org/sqlite`, the default) or **Postgres** (pgx through `database/sql`, required for HA).
- The engine is selected by `DB_CONNECTION_STRING`, as in Pocket ID.

**Migrations**
- Split per engine, like Pocket ID: `backend/resources/migrations/{sqlite,postgres}/`, goose, embedded.
- Every schema change adds a matching file to both.
- On Postgres, goose takes an advisory lock.

**Queries: one portable `queries.sql` per module, generated once**
- **Generation:** sqlc generates a single package per module (e.g. `internal/jobs/jobsdb`) with the `postgresql` engine and `sql_package: database/sql`. Queries use `sqlc.arg(name)` placeholders.
- **SQLite bridge:** `internal/database` wraps the SQLite connection in a `DBTX` that rewrites `$n` placeholders to SQLite's numbered `?n` form, once per statement (cached). The same generated code then runs on both engines.
- **Portable-SQL rules:**
  - Only the common subset: CTEs, `RETURNING`, `ON CONFLICT … DO UPDATE`, `LIMIT/OFFSET`, `COALESCE`, `lower()`.
  - No `ILIKE`, no JSON operators, no `ANY`/`sqlc.slice`. Dynamic `IN` filters go through `listquery` (§12.1).
  - No engine-specific functions.
- **CI checks:**
  - `sqlc compile` also runs against the SQLite engine, to catch non-portable queries.
  - Every module's service tests run on both engines.
- **Escape hatch:** a query that truly needs engine-specific SQL lives in hand-written code behind a small interface in that module. This should be rare.

**Francis tables** are created and migrated by Francis in the same database. Our migrations never touch them.

The schema, shown in the Postgres form (the SQLite migrations use `BLOB` for `BYTEA`):

```sql
-- instance-wide
CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);   -- instance ID etc. (Pocket ID pattern)
CREATE TABLE workspaces (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at BIGINT NOT NULL);
CREATE TABLE users (
  id TEXT PRIMARY KEY, oidc_subject TEXT NOT NULL UNIQUE, email TEXT, name TEXT,
  created_at BIGINT NOT NULL, last_login_at BIGINT
);
-- workspace_members (workspace_id, user_id, role) and workspace_invites, see §3.5

-- tenant-owned (every row has workspace_id)
CREATE TABLE providers (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('anthropic','openai')),   -- openai = any OpenAI-compatible
  base_url TEXT, api_key_enc BYTEA, key_id TEXT, created_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);
CREATE TABLE models (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model TEXT NOT NULL, label TEXT,
  price_in BIGINT, price_out BIGINT, price_cache_read BIGINT, price_cache_write BIGINT, -- micro-USD / 1M tokens
  context_window BIGINT, caps TEXT NOT NULL DEFAULT '{}',
  UNIQUE (provider_id, model)
);
CREATE TABLE settings (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  key TEXT NOT NULL, value TEXT NOT NULL,     -- default models, limits, spend cap, retention, …
  PRIMARY KEY (workspace_id, key)
);
CREATE TABLE secrets (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL, value_enc BYTEA NOT NULL,
  key_id TEXT NOT NULL,                       -- which key encrypted it: enables rotation and per-workspace keys later
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, name)
);
CREATE TABLE mcp_servers (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL, description TEXT,
  transport TEXT NOT NULL CHECK (transport IN ('stdio','http')),
  command TEXT, args TEXT, env TEXT,          -- stdio (in sandbox)
  url TEXT, headers TEXT,                     -- http (from host)
  oauth_config TEXT NOT NULL DEFAULT '{}',    -- optional OAuth client (§8)
  oauth_supported BOOLEAN,                    -- detection result, NULL until it succeeded
  oauth_credentials BYTEA, oauth_key_id TEXT, -- encrypted login
  oauth_expires_at BIGINT, oauth_refreshable BOOLEAN NOT NULL DEFAULT FALSE, oauth_logged_in_at BIGINT,
  oauth_pending BYTEA,                        -- encrypted started login, until the browser comes back
  tools_cache TEXT, tools_cached_at BIGINT, enabled BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE (workspace_id, name)
);
CREATE TABLE jobs (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL, instruction TEXT NOT NULL,
  spec TEXT NOT NULL, spec_overrides TEXT NOT NULL DEFAULT '{}',
  model_id TEXT REFERENCES models(id),
  image TEXT,                                 -- base image when the playbook has no Dockerfile
  network TEXT NOT NULL DEFAULT 'internet' CHECK (network IN ('none','internet')),
  run_as_root BOOLEAN NOT NULL DEFAULT FALSE,
  limits TEXT NOT NULL DEFAULT '{}',          -- timeout_s, max_turns, max_cost, cpus, memory_mb
  self_improve BOOLEAN NOT NULL DEFAULT TRUE,
  graduate BOOLEAN NOT NULL DEFAULT TRUE,     -- off keeps every run on the agent
  playbook_version BIGINT NOT NULL DEFAULT 0,
  concurrency TEXT NOT NULL DEFAULT 'skip' CHECK (concurrency IN ('skip','queue','parallel')),
  cron TEXT, timezone TEXT,
  next_run_at BIGINT,                         -- display mirror of the job actor's alarm (Francis owns the schedule)
  webhook_token_hash TEXT,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_by TEXT REFERENCES users(id),
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, archived_at BIGINT
);
CREATE INDEX jobs_ws_created ON jobs (workspace_id, created_at DESC);
CREATE TABLE runs (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  number BIGINT NOT NULL,
  status TEXT NOT NULL,     -- queued|provisioning|running|verifying|succeeded|failed|cancelled|timed_out|skipped
  mode TEXT NOT NULL, fell_back BOOLEAN NOT NULL DEFAULT FALSE,
  trigger TEXT NOT NULL,    -- manual|schedule|webhook|api|retry
  triggered_by TEXT REFERENCES users(id),
  input TEXT, playbook_version BIGINT NOT NULL, model_id TEXT, image_id TEXT,
  host_id TEXT, heartbeat_at BIGINT, broker_token_hash TEXT,            -- HA: who executes it, is it alive (§3.4)
  sandbox_adapter TEXT, sandbox_id TEXT, sandbox_isolation TEXT,
  queued_at BIGINT NOT NULL, started_at BIGINT, finished_at BIGINT,
  ms_queue BIGINT, ms_provision BIGINT, ms_llm BIGINT, ms_tools BIGINT, ms_total BIGINT,
  turns BIGINT NOT NULL DEFAULT 0,
  tok_in BIGINT NOT NULL DEFAULT 0, tok_out BIGINT NOT NULL DEFAULT 0,
  tok_cache_read BIGINT NOT NULL DEFAULT 0, tok_cache_write BIGINT NOT NULL DEFAULT 0,
  cost BIGINT NOT NULL DEFAULT 0,             -- micro-USD
  summary TEXT, outputs TEXT, error TEXT,
  reflection TEXT NOT NULL DEFAULT 'skipped', -- skipped|pending|done|failed
  reflection_cost BIGINT NOT NULL DEFAULT 0, verify_cost BIGINT NOT NULL DEFAULT 0,
  UNIQUE (job_id, number)
);
CREATE INDEX runs_ws_queued ON runs (workspace_id, queued_at DESC, id);   -- default table sort
CREATE INDEX runs_ws_status ON runs (workspace_id, status, queued_at DESC);
CREATE INDEX runs_job_queued ON runs (job_id, queued_at DESC);
CREATE INDEX runs_live ON runs (status, heartbeat_at);                    -- RunReconciler
CREATE TABLE api_tokens (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, created_by TEXT REFERENCES users(id),
  created_at BIGINT NOT NULL, last_used_at BIGINT, expires_at BIGINT
);

-- children, scoped through their parent
CREATE TABLE job_mcp_servers (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  mcp_server_id TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
  allowed_tools TEXT,                         -- NULL = all
  PRIMARY KEY (job_id, mcp_server_id)
);
CREATE TABLE job_secrets (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  secret_id TEXT NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
  env_name TEXT NOT NULL, PRIMARY KEY (job_id, env_name)
);
CREATE TABLE job_state (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  key TEXT NOT NULL, value TEXT NOT NULL, updated_at BIGINT NOT NULL,
  PRIMARY KEY (job_id, key)
);
CREATE TABLE playbook_versions (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  version BIGINT NOT NULL, content TEXT NOT NULL, ops TEXT, summary TEXT,
  dockerfile_hash TEXT,                       -- sha256 of content.dockerfile, NULL if none
  author TEXT NOT NULL CHECK (author IN ('reflection','user','rollback')),
  author_user_id TEXT REFERENCES users(id),
  source_run_id TEXT, created_at BIGINT NOT NULL,
  PRIMARY KEY (job_id, version)
);
CREATE TABLE images (
  id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  dockerfile_hash TEXT NOT NULL,
  base_digest TEXT,                           -- FROM pinned to a digest, reused by every replica
  status TEXT NOT NULL CHECK (status IN ('queued','building','ready','failed')),
  ref TEXT, digest TEXT, size_bytes BIGINT, error TEXT,
  log_key TEXT,                               -- build log in FileStorage
  created_at BIGINT NOT NULL, started_at BIGINT, finished_at BIGINT
);
CREATE INDEX images_job_hash ON images (job_id, dockerfile_hash, created_at DESC);
CREATE TABLE run_events (
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq BIGINT NOT NULL, ts BIGINT NOT NULL, type TEXT NOT NULL,
  span_id TEXT, parent_span_id TEXT, ms BIGINT,
  payload TEXT NOT NULL,                      -- truncated to 16 KB; full blobs in FileStorage runs/<id>/…
  PRIMARY KEY (run_id, seq)
);
```
**Event types:** `run.status · sandbox.image_wait · sandbox.create · sandbox.setup · sandbox.destroy · llm.call · tool.call · tool.result · mcp.call · broker.call · script.step · verify.result · fallback · finish · error`. Spans (`span_id`/`parent_span_id`, `ms`) drive the waterfall view.

**Setup**
- **SQLite:** WAL, `busy_timeout=5000`, `foreign_keys=on`, one writer connection plus a reader pool (the Francis provider uses the writer handle).
- **Postgres:** one pool, shared with Francis.

**Retention:** events and blobs older than the workspace's retention (default 90 days) are pruned by `RetentionPrune`. Run rows are **kept forever** for trend charts.

---

## 12. API

REST under `/api`, defined with Huma inside each module's `RegisterRoutes`.
- `umpteenth openapi` exports OpenAPI 3.1, which generates the TS types.
- Live updates use SSE via Huma's `sse` package, fed by the event bus (§3.4).
- Every route below is implicitly scoped to the caller's workspace.

```
Auth        GET  /api/auth/login · GET /api/auth/callback · POST /api/auth/logout · GET /api/users/me
Jobs        GET  /api/jobs?page=&pageSize=&sort=&search=&enabled=&mode=
            POST /api/jobs · GET/PATCH/DELETE /api/jobs/{id}   # PATCH merges: omitted fields keep their value, "" clears an optional one
            POST /api/jobs/compile                     # preview spec from text (no save)
            POST /api/jobs/{id}/runs                   # run now {input?, instructions?}
            GET  /api/jobs/{id}/stats?range=30d
            GET  /api/jobs/{id}/state?page=&pageSize=&sort=&search=   · PUT/DELETE /api/jobs/{id}/state/{key}
Playbook    GET  /api/jobs/{id}/playbook               # current
            PUT  /api/jobs/{id}/playbook               # manual edit → new version (Dockerfile change → build)
            GET  /api/jobs/{id}/playbook/versions?page=&pageSize=   · GET …/versions/{v} (+diff to previous)
            POST /api/jobs/{id}/playbook/rollback {version}
Images      GET  /api/jobs/{id}/images?page=&pageSize=&status=      · GET /api/jobs/{id}/images/{imageId} (includes the build log; the UI polls it while building)
            POST /api/jobs/{id}/images/rebuild         # re-resolve FROM, build again
Runs        GET  /api/runs?page=&pageSize=&sort=&search=&status=&job=&mode=&trigger=&from=&to=
            GET  /api/runs/{id} · GET /api/runs/{id}/events?after=
            GET  /api/runs/{id}/stream                 # SSE: replay from Last-Event-ID, then live
            POST /api/runs/{id}/cancel · /retry · /reflect
            GET  /api/runs/{id}/artifacts[/{path}]
Stats       GET  /api/stats/overview?range=7d          # counts by status/day, p50/p95 duration, spend
Live        GET  /api/events                           # SSE: run status changes (dashboard, tables)
System      GET  /api/system/info                      # version, database engine, allowPrivateNetworkTargets, and the active adapter's Info
Config      CRUD /api/providers · /api/models · /api/mcp-servers (+ POST /{id}/test, POST /{id}/oauth/login, GET /{id}/oauth/callback, DELETE /{id}/oauth)
            CRUD /api/secrets (values write-only) · /api/settings · /api/tokens   (list endpoints per §12.1)
Webhooks    POST /hooks/{jobId}                        # Bearer job token, rate-limited
Test        (e2etest build tag, never in production)
            POST /api/test/reset · /api/test/session · /api/test/llm-script · /api/test/jobs/{id}/fire-schedule
Broker      (separate listener :8081, per-run token)
            GET /v1/mcp/tools · POST /v1/mcp/call · POST /v1/llm
            GET/PUT /v1/state/{key} · POST /v1/output · POST /v1/step · POST /v1/fail
```

**Auth (v1): OIDC only.**
- Any user the IdP lets through the client gets into the single default workspace. Access is restricted in the IdP (e.g. Pocket ID's allowed user groups), optionally narrowed by `OIDC_ALLOWED_GROUPS` against the `groups` claim.
- Each login upserts a `users` row.
- Sessions and the in-flight login state are stateless signed cookies, keyed from `ENCRYPTION_KEY`, so any replica can serve any request.
- API tokens (hashed, optional expiry) are workspace-scoped and cover `/api` automation. Webhooks use per-job tokens.

### 12.1 List endpoints (server-side tables)

Every list endpoint takes the same parameters and returns the same envelope. That covers runs, jobs, MCP servers, secrets, tokens, playbook versions and images. This way every TanStack table in the UI (§13) works identically, and nothing is paginated, sorted or searched in the browser.

**Request parameters**
- **Common:** `httpserver.ListParams`, embedded in every list input struct:
  - `page` (1-based)
  - `pageSize` (default 25, max 100)
  - `sort`: comma-separated keys, with a `-` prefix for descending, e.g. `sort=-queued_at,cost`
  - `search`: free text
- **Typed filters per endpoint** (e.g. `status=failed,running`, `job=<id>`, `mode=`, `trigger=`, `from=`, `to=`). They are real query parameters, so they appear in the OpenAPI spec and the generated TS types.

**Response:** `Paginated[T]{ items, page, pageSize, total }`.

**Backend: `internal/listquery`**
- **Query building:** it builds the `SELECT … WHERE workspace_id = … AND <filters> AND <search> ORDER BY … LIMIT … OFFSET …` and the matching `COUNT(*)`. It works from the module's whitelist in `list.go`, which maps each sortable key to a SQL expression and each filter to a SQL fragment, and lists the searchable columns. Rows are scanned into the module's sqlc row types.
- **Validation:** unknown sort keys or filters return `validation_failed`, so the client can't sort or filter by arbitrary columns.
- **Stable pages:** `id` is always appended as a tiebreaker to `ORDER BY`.
- **Search:** portable `lower(col) LIKE lower(@search)` over the whitelisted columns. Postgres trigram or SQLite FTS can come later behind the same parameter.
- **Large tables:** a spec can opt into four techniques, which `runs` uses to stay under the M2 target with 100k rows:
  - `Key`: the page query first picks the page's keys (`WHERE r.id IN (SELECT r.id … ORDER BY … LIMIT …)`), so a sort on an unindexed column never sorts full rows.
  - `CountFrom`: the count leaves out joins that only add display columns.
  - `JoinSorts`: only sort keys that need a joined table (e.g. the job name) pick keys through the join. The join also repeats the workspace condition, so the planner can walk the workspace's jobs by name and stop after one page.
  - `SearchConds`: search on a joined table's column goes through an uncorrelated subquery (`r.job_id IN (SELECT id FROM jobs WHERE …)`), because an `OR` across a join makes SQLite scan badly.
- **SQLite tuning:** connections use `mmap_size` = 256 MB and a 32 MB page cache, which keeps scans off the syscall path.
- **Indexes:** every sortable or filterable column used by a default view has an index (see `runs_*` in §11).

**Pagination style:** offset pagination with a total count. It fits TanStack's page model, and the indexes keep it fast for the volumes a workspace produces. If `runs` ever outgrows it, that endpoint can switch to keyset pagination (next/previous only) without changing the table component.

---

## 13. UI (SvelteKit + shadcn-svelte)

**Stack**
- **Framework:** SvelteKit 2 with `adapter-static` (`fallback: 'index.html'`, output `../backend/frontend/dist`, `precompress: true`, `ssr = false`), and Svelte 5 runes only.
- **Styling and components:** Tailwind v4, shadcn-svelte, `@lucide/svelte`, `mode-watcher` (dark mode), `svelte-sonner`.
- **Data and forms:** zod v4 (`import { z } from 'zod/v4'`), `openapi-typescript` + `openapi-fetch` for a typed client, **TanStack Table** via shadcn-svelte's data-table.
- **Charts and code:** LayerChart via shadcn charts, CodeMirror 6 for editing instructions, scripts and Dockerfiles, and Shiki (lazy-loaded) for read-only code and log highlighting.
- **Language:** English only for v1, no i18n library.

**Conventions (from Pocket ID):**
- **Services:** `lib/services/*-service.ts` has one class per domain (`JobService`, `RunService`, …), each extending an `APIService` base that holds the typed `openapi-fetch` client.
- **Types:** API types are generated into `lib/api/schema.d.ts` and never hand-written. `lib/types/` is only for UI-only types.
- **Forms:** `createForm(schema, initial)` from `lib/utils/form-util.ts` with `form-input.svelte`. Unsaved-changes tracking as in Pocket ID.
- **Errors:** `lib/utils/error-util.ts` maps `apperror` codes to messages and shows `toast.error`. There is also a `tryCatch` result helper.
- **Confirm dialog:** store-driven, mounted once in the root layout.
- **Auth guard:** the root `+layout.ts` loads `/api/users/me` and redirects to `/api/auth/login` when there is no session.
- **Colocation:** page-specific components live next to their route. Detail pages use `[id]/+page.ts` loaders that call services.
- **Dev:** `vite dev --port 3000` proxies `/api` and `/hooks` to the backend.

**Tables (TanStack, fully server-side)**
- **Shared `DataTable` component:** built on shadcn-svelte's data-table with `manualPagination`, `manualSorting` and `manualFiltering` all enabled. `rowCount` comes from the response's `total`. The browser never sorts, filters or pages data itself.
- **One adapter from state to API:** TanStack state (pagination, sorting, column filters, global filter) is mapped to the §12.1 parameters and passed to a `fetchPage(params)` callback, which is the service method. Each table only declares its columns and which of them are sortable or filterable.
- **URL is the state:** table state is synced to the page's query string, so filtered views are linkable and survive reload and back/forward.
- **Search** is debounced (300 ms) and resets to page 1.
- **Filters:** faceted filters (status, mode, trigger, job) and the date range map to the typed filter parameters.
- **Loading:** skeleton rows are shown on the first load. Later loads keep the previous rows until the new page arrives, so the table doesn't flicker.
- **Live updates** without reshuffling:
  - SSE status events patch rows that are on the current page in place.
  - New runs raise an "N new runs" pill, which refetches on click.
  - Refetching is automatic only on page 1 with the default sort.

**Visual language**
- Neutral zinc base, generous whitespace, and monospace (Geist Mono) for logs, commands and numbers.
- **Mode colors:** Explore = violet, Assisted = blue, Scripted = emerald.
- **Status colors:** succeeded green, failed red, running animated blue, cancelled/skipped muted.
- **Formats:** durations as `1m 12s`, cost as `$0.014`, tokens as `12.4k`.

**Layout:** a shadcn `Sidebar` (Dashboard, Runs, Jobs, MCP Servers, Settings), plus a top bar with a breadcrumb and a `⌘K` command palette (jump to a job/run, "Run job…", "New job").

| Route | Content |
|---|---|
| `/` Dashboard | KPI cards: runs (period), success rate, p50 duration, spend (with Δ vs previous period, split run vs learning cost). Stacked bar chart of runs/day by status. Area chart of cost/day by job. **Running now** list with live elapsed timers. Recent failures. **"Getting cheaper"**: jobs with the biggest cost-per-run drop |
| `/runs` | Server-side table: status, job, #, mode badge, trigger icon, started (relative, exact on hover), **duration with a stacked mini-bar** (queue / image wait / sandbox / LLM / tools), tokens, cost, model. Search, faceted filters (job, status, mode, trigger), date range, live row updates via SSE |
| `/runs/[id]` | Header: status, duration, cost, tokens, mode, playbook version link, image digest, trigger + who, sandbox isolation badge, Cancel/Retry/"Learn from this run". Tabs: **Timeline** (vertical steps: LLM turns with collapsible text, tool calls with args and terminal-styled output, MCP/broker calls, `ump step` markers; each with duration + tokens; live streaming with auto-scroll) · **Waterfall** (horizontal span bars showing where the time went) · **Outputs** (structured outputs, artifacts, `finish` summary rendered as markdown) · **Learned** (playbook diff produced by this run's reflection) · **Raw** (JSON download) |
| `/jobs` | Server-side table: name, schedule in plain words ("Weekdays 08:00"), mode badge, last run status, success-rate sparkline, **cost-per-run sparkline**, next run, enabled switch |
| `/jobs/new` | One big textarea "Describe the job" → **Compile** → spec cards (schedule, success criteria, MCP servers matched, inputs/outputs, proposed Dockerfile, side effects, warnings) → Save / Save & run |
| `/jobs/[id]` | Tabs: **Overview** (instruction, spec, the **graduation chart**: bars = cost per run coloured by mode, line = duration, markers for playbook versions) · **Runs** · **Playbook** (learnings list with hits/source run/edit/retire; toolkit scripts in a code viewer with stats; main + verify; **version history with diffs and rollback**) · **Environment** (Dockerfile editor, build status and live log, image size/digest, image history table, Rebuild) · **State** (key-value viewer/editor) · **Settings** (model, base image, limits, network, root, MCP servers + tool allow-list, secrets→env, triggers + webhook URL/token, **self-improve toggle**, mode pin, concurrency) |
| `/mcp` | Server list with status and auth (OAuth, not logged in, bearer token), Add (stdio command / HTTP URL + headers + optional OAuth client), **Test** → tool list with annotations, **Log in** / **Log out** |
| `/settings` | Providers & models (prices, capabilities), default models by role, **sandbox** (read-only: active adapter, version, arch, isolation, capabilities; editable defaults for image and limits), concurrency, secrets, API tokens, spend cap, retention |

shadcn-svelte components used: sidebar, breadcrumb, command, card, badge, button, data-table, table, pagination, tabs, chart, dialog, sheet, dropdown-menu, select, switch, textarea, input, tooltip, hover-card, collapsible, scroll-area, resizable, skeleton, separator, sonner, alert, popover, calendar (date-range filter).

---

## 14. Repository layout

```
umpteenth/
├── backend/                           # Go module
│   ├── cmd/
│   │   ├── umpteenth/main.go          # server + cobra subcommands
│   │   └── ump/main.go                # sandbox CLI (CGO_ENABLED=0, linux/amd64 + arm64)
│   ├── frontend/                      # go:embed of dist/ + SPA fallback (exclude_frontend → stub)
│   ├── resources/migrations/
│   │   ├── sqlite/                    # goose migrations, SQLite timeline
│   │   └── postgres/                  # goose migrations, Postgres timeline (also sqlc's schema)
│   ├── internal/
│   │   ├── bootstrap/                 # wires DB, storage, actor host, sandbox adapter, modules, router, servicerunner
│   │   ├── cmds/                      # cobra: serve, healthcheck, openapi, export/import, version
│   │   ├── common/                    # EnvConfig, version
│   │   ├── apperror/                  # coded, client-safe errors (from Pocket ID)
│   │   ├── httpserver/                # Huma API setup, error rendering, ListParams/Paginated, SSE helpers
│   │   ├── middleware/                # session + API-token auth, workspace resolution, CSP, cache control
│   │   ├── database/                  # SQLite/Postgres open, placeholder-rewriting DBTX, goose runner + lock
│   │   ├── listquery/                 # whitelisted server-side pagination/sort/search/filter builder
│   │   ├── storage/                   # FileStorage: filesystem, s3, database (from Pocket ID)
│   │   ├── instanceid/                # instance ID in kv (sandbox ownership)
│   │   ├── testutil/                  # NewDatabaseForTest (both engines), NewActorHostForTest, fixtures
│   │   │
│   │   │  # feature modules: module.go · handler.go · service.go · dto.go · models.go · queries.sql · <x>db/ · list.go · actor.go
│   │   ├── auth/                      # OIDC login, sessions, users
│   │   ├── workspaces/                # default workspace bootstrap, (later) members and roles
│   │   ├── apitokens/
│   │   ├── settings/
│   │   ├── secrets/                   # AES-256-GCM with ENCRYPTION_KEY, key_id per secret
│   │   ├── providers/                 # LLM providers, models, prices → llm.Provider instances
│   │   ├── mcpservers/                # registry, test, tools cache
│   │   ├── jobs/                      # CRUD, compile step, job actor, webhooks, job state
│   │   ├── images/                    # job image builds (image-builds taskpool), GC, logs
│   │   ├── runs/                      # records, events API, SSE, cancel/retry, runs taskpool, reconciler
│   │   ├── playbook/                  # versions, ops, render, reflection taskpool, graduation, header parser
│   │   ├── stats/
│   │   ├── system/                    # sandbox info endpoint, health
│   │   ├── broker/                    # sandbox-facing API (own listener)
│   │   ├── e2etest/                   # test endpoints (e2etest tag)
│   │   │
│   │   │  # engine packages (no routes)
│   │   ├── runner/                    # run lifecycle, modes, verifier, fallback, heartbeat
│   │   ├── agent/                     # loop, builtin tools, prompt builder, budgets, truncation
│   │   ├── llm/                       # Provider interface, pricing; anthropic/, openai/, fake/
│   │   ├── sandbox/                   # Adapter + ImageBuilder interfaces, types, capabilities, ump binary embed
│   │   │   ├── docker/                # Docker/Podman adapter (v1)
│   │   │   ├── fake/                  # in-memory adapter for unit tests
│   │   │   └── sandboxtest/           # conformance suite every adapter must pass
│   │   ├── mcp/                       # client manager, in-sandbox stdio transport
│   │   └── events/                    # recorder (DB) + bus: local (SQLite) | Postgres LISTEN/NOTIFY
│   ├── sqlc.yaml · .golangci.yml · Makefile
├── frontend/                          # SvelteKit app → builds into backend/frontend/dist
├── tests/                             # Playwright e2e (drives the Dockerized full stack)
│   ├── setup/docker-compose.yml       # + docker-compose-ha.yml (Postgres, 2 replicas, SeaweedFS)
│   ├── specs/ · specs/fixtures/ · utils/
│   └── playwright.config.ts
├── docker/
│   ├── Dockerfile                     # multi-stage: frontend → ump CLI → server (distroless/static)
│   └── sandbox/Dockerfile             # umpteenth-sandbox image
├── docker-compose.yml
├── package.json · pnpm-workspace.yaml # workspace: frontend, tests
├── AGENTS.md
└── PLAN.md
```
**Tooling (Pocket ID style, no Taskfile)**
- **`backend/Makefile`:**
  - `test`: `go test -tags exclude_frontend,unit ./...` on SQLite, and on Postgres with `TEST_DB=postgres`
  - `test-integration`, `lint`
  - `gen`: sqlc, plus the SQLite `sqlc compile` check
  - `ump`: cross-compile the CLI
- **Root `package.json`** scripts filter into the workspaces: `dev`, `build`, `check`, `format`, `gen:api` (`umpteenth openapi` → `openapi-typescript`), `test`.
- **Local dev:** the backend runs with `go run -tags exclude_frontend,exclude_ump ./cmd/umpteenth`, next to `pnpm dev`. A VS Code compound launch config starts both.
- **CI** runs:
  - `go vet` and `golangci-lint`
  - unit tests on both engines
  - the sandbox conformance suite against Docker and Podman
  - `svelte-check`
  - the Playwright suite in both topologies (§17)
  - a build

---

## 15. Configuration & deployment

Every secret supports a `*_FILE` variant. Settings marked *default* are the defaults for new workspaces, which can override them.

| Env | Default | Purpose |
|---|---|---|
| `APP_ENV` | `production` | `production` / `development` / `test` |
| `APP_URL` | `http://localhost:8080` | Public URL (OIDC callback, webhook URLs) |
| `PORT` / `BROKER_PORT` | `8080` / `8081` | UI+API / sandbox broker |
| `DB_CONNECTION_STRING` | `data/umpteenth.db` | SQLite file path, or `postgres://…` for Postgres |
| `HA_ENABLED` | `false` | Allow multiple replicas. Requires Postgres and a shared `FILE_BACKEND` (`s3`, `database`, or a shared volume) |
| `FILE_BACKEND` | `filesystem` | `filesystem` / `s3` / `database` for blobs (full tool outputs, artifacts, build logs). `S3_*` vars as in Pocket ID |
| `DATA_DIR` | `./data` | Base directory for the SQLite file and filesystem blobs |
| `ENCRYPTION_KEY` | *required* | Secrets and API keys at rest, session signing, Francis PSK derivation |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | *required* | Login |
| `OIDC_ALLOWED_GROUPS` | unset | Optional `groups`-claim check on top of the IdP's own restrictions |
| `SANDBOX_ADAPTER` | `docker` | Active sandbox adapter |
| `SANDBOX_IMAGE` | `ghcr.io/stonith404/umpteenth-sandbox:latest` | Default base image |
| `SANDBOX_REGISTRY` | unset | Registry for job images. Shares builds across replicas; required for Kubernetes |
| `SANDBOX_DOCKER_RUNTIME` | `runc` | Docker adapter: `runsc` for gVisor |
| `SANDBOX_DOCKER_DNS` | unset, `8.8.8.8,8.8.4.4` under gVisor | Docker adapter: resolvers of unrestricted sandboxes instead of Docker's embedded DNS, which gVisor can't reach. `ump init` writes them to `/etc/resolv.conf` |
| `SANDBOX_ALLOW_UNRESTRICTED_NETWORK` | `true` | Docker adapter: offer jobs the `unrestricted` network, which bypasses the egress proxy |
| `NETWORK_BLOCKED_TARGETS` | unset | IPs and CIDR ranges that neither the host-side egress guard nor the sandboxes' egress proxy may reach, e.g. the host's public address |
| `DOCKER_HOST` | Docker default | Docker adapter: also works with a Podman socket |
| `MAX_CONCURRENT_RUNS` | `3` | Runs per replica (the `runs` taskpool's concurrency) |
| `DAILY_SPEND_LIMIT_USD` | unset | *default* per-workspace daily spend cap |
| `RUN_RETENTION_DAYS` | `90` | *default* retention for events and blobs |
| `ALLOW_PRIVATE_NETWORK_TARGETS` | `true` | Host-side egress guard (§3.5). Must be `false` for SaaS. Reported by `/api/system/info` so the UI can warn before a save fails |
| `TRUST_PROXY` | `false` | Take client IPs for rate limits from the last `X-Forwarded-For` hop. Only enable behind a reverse proxy that sets it |
| `REPLICA_ID` | hostname | Stable name of this replica, used for sandbox labels and the relay name |
| `ACTORS_HOST` / `ACTORS_PORT` | `127.0.0.1` / `7571` | Address other replicas reach this replica's Francis peer server at (UDP). Set `ACTORS_HOST` to the replica's hostname with HA |
| `ACTORS_BIND_ADDRESS` | all interfaces with HA, else `ACTORS_HOST` | Interface the peer server listens on. Binding everywhere keeps a replica reachable when its hostname resolves to another network, e.g. a sandbox network Docker restores after a crash |
| `LOG_LEVEL` | `info` | slog level |

```yaml
# docker-compose.yml: single node, SQLite, no database server
services:
  umpteenth:
    image: ghcr.io/stonith404/umpteenth:latest
    restart: unless-stopped
    ports: ["8080:8080"]
    environment:
      APP_URL: https://umpteenth.example.com
      ENCRYPTION_KEY: ${ENCRYPTION_KEY}
      OIDC_ISSUER: https://id.example.com
      OIDC_CLIENT_ID: umpteenth
      OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET}
    volumes:
      - ./data:/app/data
      - /var/run/docker.sock:/var/run/docker.sock   # or rootless Podman socket / socket proxy
```
**HA variant:**
- `DB_CONNECTION_STRING=postgres://…`, `HA_ENABLED=true`, `FILE_BACKEND=s3`, and `SANDBOX_REGISTRY` set.
- Two or more replicas behind a load balancer that allows long-lived SSE connections.
- Each replica talks to its own (or a shared) Docker daemon.

**On startup** each replica:
1. Runs migrations (under a lock on Postgres), loads the instance ID, and ensures the default workspace exists.
2. Starts the actor host, which joins the cluster when HA is enabled.
3. Builds the configured sandbox adapter and calls `Check`, failing fast with a clear message.
4. Lets the adapter prepare its backend. For Docker:
   - detect its own container ID (container mode), or start `umpteenth-relay` (binary mode)
   - pre-pull the default image
5. Starts the per-replica reaper.

---

## 16. Milestones & acceptance criteria

### M0: Skeleton (≈ 1.5 weeks)
- Repo layout (`backend/`, `frontend/`, `tests/`), pnpm workspace, `backend/Makefile`, CI.
- `common.EnvConfig`, `bootstrap` + `servicerunner`, `apperror`, and `httpserver` (Huma + error rendering + `ListParams`).
- **SQLite and Postgres**: `database` with the placeholder-rewriting `DBTX`, per-engine goose migrations, sqlc, the SQLite compile check, and `NewDatabaseForTest` for both engines.
- `FileStorage`, `instanceid`, and the embedded Francis host (single host on SQLite, cluster-capable on Postgres).
- Workspaces + users, workspace resolution in the middleware, and the tenant-scoping query test.
- **OIDC login + sessions**, health + settings.
- SvelteKit shell: sidebar layout, dark mode, typed client + `APIService`, `createForm`, `error-util`, and the TanStack `DataTable` (used first by API tokens).
- Embed + SPA fallback, and the multi-stage Dockerfile.
- Playwright harness: `tests/setup/docker-compose.yml`, `/api/test/reset`, and `/api/test/session`.

**Done when:**
- `docker compose up` serves the UI behind OIDC login from one container.
- The image is < 50 MB.
- Unit tests pass on SQLite and Postgres.
- A Playwright smoke test (session → empty dashboard → create and list an API token) passes.

### M1: Sandbox + agent loop, both providers (≈ 2 weeks)
- `sandbox` package: interfaces, contract, `fake` adapter, and the `sandboxtest` conformance suite.
- **Docker/Podman adapter** passing the suite: create/exec/files/destroy, the `ump exec` shim, limits, security defaults, per-run networks, and the relay for binary mode.
- The default sandbox image and the per-replica reaper.
- `Provider` interface with **anthropic** and **openai-compatible** adapters, plus `fake`; pricing (micro-USD) and usage normalization.
- Agent loop with builtin tools, truncation, budgets and `finish`.
- `runs` taskpool + runner with heartbeat, and the `RunReconciler`.
- Event recorder + bus (local and Postgres NOTIFY) + SSE.
- Jobs with a raw instruction (no compile yet) and **Run now**; the run detail page with a live Timeline.

**Done when:**
- "Find the 3 largest files in /usr and output their paths" runs end-to-end on `claude-opus-5-5` **and** on a local Ollama model.
- The timeline streams live, and cost and durations are recorded.
- Cancelling destroys the sandbox within 2 s.
- The conformance suite passes against Docker and rootless Podman.

### M2: Jobs, triggers, environments, observability (≈ 2.5 weeks)
- Compile step + spec UI (`/jobs/new`), including capability warnings and proposed Dockerfiles.
- **Job actor**: cron alarms with timezones, and the concurrency policies.
- Webhooks (with rate limit) and workspace-scoped API tokens.
- Job state (`state_get/set`).
- **Job images**: the Dockerfile in the playbook, the `image-builds` taskpool, the Docker `ImageBuilder`, the Environment tab with live build log, `SANDBOX_REGISTRY`, and `ImageGC`.
- `listquery` + §12.1 on all list endpoints; Runs and Jobs as server-side TanStack tables with search, filters and live rows.
- Dashboard KPIs + charts; job stats; waterfall view; the retention cron job.

**Done when:**
- A job written as "every 5 minutes, record the current BTC price and output the change since last run" schedules itself from the text alone, uses job state across runs, and shows up correctly on the Dashboard and Runs pages.
- A job whose Dockerfile installs `ffmpeg` builds once, and later runs start without installing anything.
- The Runs table pages, sorts, searches and filters 100k seeded runs server-side with p95 < 150 ms per request on both engines.
- **HA check:** with two replicas on Postgres, the 5-minute job fires exactly once per slot across a replica restart, and runs spread across both replicas.

### M3: MCP + broker + `ump` CLI (≈ 2 weeks)
- MCP registry UI + test, with the host-side egress guard.
- HTTP servers from the host; stdio servers inside the sandbox as user `mcp`; per-job allow-lists; MCP tools in the agent loop.
- Broker listener with hashed per-run tokens, reachable in both Docker deployment modes and always targeting the executing replica.
- `ump` CLI (`mcp`, `llm`, `state`, `output`, `step`, `fail`) embedded and injected; secrets→env.

**Done when:**
- The stale-PR example job works with GitHub (HTTP) and Slack (stdio via `npx`) MCP servers.
- `ump mcp call github …` works from a script inside the sandbox, in container mode and in binary mode.
- The GitHub token never appears in the container env.
- With two replicas, cancel and the live timeline work when the UI and the run are on different replicas, and killing the executing replica mid-run marks the run `interrupted` within ~75 s.

### Status of M0–M6

M0–M3 were implemented and verified on 2026-09-25:
- **Tests:** unit tests on SQLite and Postgres, the Docker sandbox suite, and 15 Playwright specs, all against both the single-node stack and a two-replica HA stack behind a load balancer.
- **M1:** the largest-files job on a local Ollama model (3 turns); cancel destroys the sandbox in < 1 s; the timeline streams live.
- **M2, compile and state:** "every 5 minutes, record the current BTC price …" compiles to `*/5 * * * *` UTC on the local model, and two runs report `change: 0`, then `change: -107` through job state.
- **M2, job images:** an ffmpeg Dockerfile builds once (40 s), and the next run provisions in 267 ms with ffmpeg present.
- **M2, list performance:** with 100k runs, every Runs table case (paging, filters, 4 sorts, search), the dashboard and the jobs list stay under p95 150 ms. The worst case is 102 ms on SQLite and 98 ms on Postgres.
- **M2 HA:**
  - An every-minute job fired exactly once per slot for 6 slots across a replica restart.
  - 8 runs spread 5/3 across two replicas.
  - With continuous job creation during a replica kill and restart, only the one call in flight to the killed replica failed (actor calls are at-most-once).
  - Placement errors are retried with backoff (up to ~8 s).
- **M3:** HTTP and stdio MCP work in container mode on the HA stack, including `ump mcp call`. The stdio server runs as uid 1001 with its secret, which the agent cannot see. Cancel and live output work across replicas (0.5–1 s). Killing the executing replica marks the run interrupted after about 20 s.

M4 was implemented and verified on 2026-09-26:
- **How it works:**
  - Reflection runs on the `reflection` taskpool after every run of a job with learning on, or once on request ("Learn from this run").
  - It reads a condensed transcript with the job's secrets redacted, the scripts the run left in `/ump/candidates`, and the setup and install time of the last runs.
  - Operations are validated one by one:
    - invalid ones are rejected, and the model gets the reasons for one more answer
    - risky ones are held for review: a new base image, `curl | sh`, `ADD` from a URL, or text containing a secret or a credential-shaped string
    - a changed Dockerfile is built before it is applied, and one that doesn't build is rejected with its build log
  - The outcome is stored on the run (Learned tab), and applied operations become a version with `author=reflection`.
  - Learning hits and script calls are counted in `playbook_stats`, outside the immutable versions.
- **Toolkit tools:** each script is a `toolkit__<name>` tool whose `ump:args` become `--name value` flags. Saving a playbook reads every script's `ump:` header, so hand edits keep the tool definition in step.
- **Eval (`tests/eval`, gpt-6-luna through an OpenAI-compatible gateway, 5 jobs × 6 runs, three runs of the eval):**
  - All 30 runs succeeded in the second and third eval, and all 30 reflections did in the third.
  - Turns dropped by at least 50% from run 1 to run 3 for 3–4 of 5 jobs. A typical job went from 4–8 turns to 2: the agent calls the promoted script, then finishes.
  - Cost and time fell with the turns, e.g. the CO2 chart job went from $0.019 and 29 s to $0.005 and 6 s.
  - The CO2 chart job moved matplotlib into its Dockerfile, which built on the first try and served every later run.
  - The first eval found two bugs, both fixed: a learned Dockerfile that didn't build left the job unable to run, and non-strict structured output let the model return malformed operations.
- **Rollback:** rolling back a learned Dockerfile makes the next run use the base image again, and the job returns to Explore mode (Playwright, real Docker builds).
- **Tests:** unit tests on SQLite and Postgres, 20 Playwright specs on both the single-node and the HA stack.

M5 was implemented and verified on 2026-09-26:
- **How it works:**
  - Reflection may propose `main` only when the last 3 runs were successful Assisted runs with at most 2 ad-hoc bash calls each and the same toolkit and MCP sequence. The runs are checked for it, so the model can't graduate a job that isn't ready.
  - A scripted run executes `/ump/main` with half the run's time. Every run must exit 0 and report the job's outputs, and `verify` adds its own checks. The utility model judges only when `verify.llm` is set.
  - When the checks fail, an Assisted agent takes over in the same sandbox. It is told why the script failed, the last `ump step`, the end of the output, the outputs so far and the MCP calls with side effects that already happened. Reflection then repairs `main` or the toolkit script it calls.
  - Two fallbacks in a row since the job graduated demote it to Assisted until it qualifies again. The job page shows the mode of the next run and the demotion.
  - Whether a run will be reflected on is stored with its final status, so a finished run never briefly looks like it won't be learned from.
- **Eval (gpt-6-luna, 6 jobs × 7 runs):**
  - 5 of 6 jobs graduated after run 4 or 5 and then ran scripted at $0, in 0.7–3 s instead of 13–47 s for their first run.
  - Self-healing without a planted failure: a transient TLS error failed a scripted CO2 download, the agent finished the job, and reflection added retries to `main`.
  - The orders job gets its amounts under a renamed field from run 8 on. It graduated after run 4 and ran scripted at $0 for runs 5–7. Run 8 fell back and finished through the agent, reflection fixed the toolkit script `main` calls, and runs 9–10 were scripted again.
  - In an earlier series the first repair rewrote an inline one-liner in `main` with a missing parenthesis, and the job was demoted after its next run failed too. Reflection is now told to repair minimally from what the agent did and to keep logic in tested toolkit scripts.
  - The first M5 eval also found a gateway outage that the retry logic didn't recognize. Connection failures now count as transient.
- **Tests:** unit tests on SQLite and Postgres, and 21 Playwright specs including the full graduation lifecycle, which also passed 13 repeated runs on the HA stack.

M6 was implemented and verified on 2026-09-26:
- **Egress:**
  - Firewall rules keep `internet` sandboxes off private ranges, the Docker host and metadata. A privileged helper container installs them in the host's namespaces, idempotently, and every maintenance tick repairs them after an engine restart. `SANDBOX_EGRESS_FILTER` decides what happens where they can't be installed, and the settings page shows the state.
  - "Allow private network" jobs use `ump-egress-private`, where only metadata stays blocked.
  - `allowlist` jobs stay on their internal network with `HTTP(S)_PROXY` pointing at a proxy on the broker listener. It authenticates with the run token, matches `example.com` and `*.example.com`, refuses IP literals and private resolutions, and records the first connection per host and every refusal on the timeline.
  - The API listener refuses connections on this replica's addresses on sandbox networks, so sandboxes reach nothing but the broker.
  - Live checks in container mode: a sandbox gets no answer from the API on :8080 and a 204 from the broker; internet sandboxes reach neither the host, the LAN nor metadata; "allow private network" reaches the host; `allowlist` reaches only its listed domain, from curl and Python alike.
- **Notifications:** a webhook for `run.failed`, `run.fell_back` and `job.demoted`, including runs the reconciler fails. Deliveries are durable taskpool tasks with 5 attempts on 5xx and 429, carry `text` and `content` for Slack and Discord, and are signed with an HMAC-SHA256 header when a signing secret is set. The settings page has a test button.
- **Export and import:** `umpteenth export [--history]` and `umpteenth import [--secrets file] [--dry-run]` work through the API with an API token. Exports hold secret names only, leave out API keys and the webhook URL, and redact credential-like MCP values while keeping `{{secret:…}}` references. Imports match by name, so a second import creates nothing and attaches secrets created since. A round trip reproduced every job, playbook version and attachment without leaking a value.
- **Context compaction:** see §6.1.
- **gVisor:** the Docker suite and a real job run pass under runsc (release 20260921, arm64, in a dind engine that `backend/scripts/gvisor-engine.sh` starts). Making them pass took four changes:
  - runsc needs `--overlay2=none --file-access=shared`, or files copied into a running sandbox and files it writes stay invisible across the boundary. A startup probe detects that and names the fix.
  - gVisor can't reach Docker's embedded DNS, so sandboxes find the broker through `/etc/hosts` and internet sandboxes get `SANDBOX_DOCKER_DNS`.
  - A command that runs out of memory stops the whole sandbox, which is now reported as out of memory.
  - A sandbox that stops takes a moment longer to show as stopped, which the adapter waits for briefly.
  - CI runs the suite under gVisor next to Docker and Podman.
- **Warm pool:** not built, see §20.
- **Docs:** an Astro Starlight site in `docs/`, published to GitHub Pages by a workflow.
- **HA example fix:** current SeaweedFS images reject signed S3 requests without an identity, which made every blob upload fail in both HA compose files. They now configure one.
- **Tests:** unit tests on SQLite and Postgres, the Docker suite under runc and runsc, and all 22 Playwright tests on the single-node stack and on two fresh HA stacks. A first HA run exposed a test race, since fixed: reflection of one run could take the fake model's answers scripted for the next.

Not yet verified: Claude through the Anthropic API (no key; the adapter is tested against recorded fixtures), rootless Podman, gVisor on x86-64 and outside dind, and the stale-PR example against real GitHub and Slack.

Known gaps, deliberately left for later:
- **No catch-up for live output:** deltas are not replayed on connect. Output printed before a client connects appears once the step's result is persisted.
- **No replay on `/api/events`:** events carry no IDs, so clients reload after a reconnect.
- **No learning from past runs in bulk:** turning learning on doesn't offer to learn from the last N runs yet (§9.3). "Learn from this run" works on any run that succeeded, failed or timed out.
- **No per-job reflection model:** reflection uses the workspace's reflection model, or the job's own model.
- **Held operations are applied by hand:** there is no one-click "apply" for an operation held for review; it is copied into the playbook editor.
- **Reflection after every Assisted run:** a successful Assisted run still costs one reflection call, even when nothing changes.
- **Shadow runs skip jobs with side effects or state:** a job whose spec lists side effects, whose scripts are marked `external`, or whose run changed its state gets a new `main` untried (§10.4), so its first test is its next real run.
- **Unpinned FROM:** a job image's FROM stays unpinned (`baseDigest` null) when the base image cannot be pulled, e.g. a local-only sandbox image.
- **No `spec_overrides` API:** the column exists, but no API exposes it yet (§7.1).
- **Only the first stage is pinned:** in a multi-stage job Dockerfile, only the first external `FROM` is pinned to a digest, and `FROM ${ARG}` isn't resolved.
- **Proxy-only egress:** tools that ignore proxy settings (UDP, ping, raw TCP without SOCKS, Node's `fetch` before 24) can't connect from `internet` sandboxes and need `unrestricted`. The allow-list matches the requested host name without inspecting TLS, so domain fronting can reach hosts off the list, and all proxied traffic flows through the Umpteenth process.
- **gVisor and relay replacement:** under gVisor the broker's address is pinned in `/etc/hosts`, so a run in flight while the relay container is replaced (an upgrade in binary mode) loses its broker.
- **Compaction is lossy:** a compacted run keeps its handover and last exchange, not the full history, so details the summary left out are gone for the rest of the run. The timeline keeps everything.
- **Sessions can't be revoked:** sessions are stateless signed cookies (§3.4), so one stays valid for up to 7 days after logout on another device or removal from `OIDC_ALLOWED_GROUPS`. Rotating `ENCRYPTION_KEY` is the only kill switch, and it also makes stored secrets unreadable.
- **The daily spend cap is checked when a run starts:** runs already going count as zero until they finish, so parallel runs can spend past the cap, and reflection cost counts on the day its run was queued.
- **ASCII-only case folding in SQLite search:** SQLite's `lower()` folds only ASCII, so a search for "über" doesn't match "Überwachung" there, while Postgres does. A Unicode-aware SQL function would blow the search latency budget.

### M4: Self-improvement (≈ 2 weeks)
- Playbook versions + ops + renderer (with size budget).
- Reflection taskpool (structured output, validation, candidates → toolkit, `set_dockerfile`).
- Toolkit scripts as tools; `remember`; the self-improve toggle; "Learn from this run".
- Playbook tab (learnings, toolkit, Dockerfile, version history, diff, rollback, manual edit) and the run "Learned" tab.

**Done when:**
- On the reference eval set (§17), **turns per run drop ≥ 50% between run 1 and run 3** for at least 3 of 5 jobs.
- At least one eval job moves a repeated install into its Dockerfile on its own.
- Rolling back a playbook version fully restores prior behavior, including the environment.

### M5: Graduation (≈ 2 weeks)
- Script header parser; graduation criteria; `propose_main` / `set_verify`.
- Scripted runner; deterministic + LLM verification.
- Fallback into an Assisted agent in the same sandbox, with the side-effect log.
- Self-healing `update_main`; demotion; mode pin; graduation chart.

**Done when:**
- At least 2 eval jobs reach Scripted mode automatically and cost ≤ 10% of their run-1 cost.
- Deliberately breaking the environment (e.g. a renamed API field) triggers fallback, the job still completes, and `main` gets repaired.

### M6: Hardening (done 2026-09-26)
- gVisor documentation + checks; egress allow-list proxy.
- Warm sandbox pool.
- Context compaction.
- Failure notifications (webhook/ntfy/email).
- Backup/export of jobs + playbooks (`umpteenth export/import`).
- Docs site, including an HA deployment guide.

### Later
- **More sandbox adapters:** Kubernetes (with an in-cluster BuildKit) and a microVM adapter (Firecracker or Kata; Apple `container` for macOS dev), built against the conformance suite with no changes to the core.
- **SaaS:** self-service sign-up, billing, per-workspace concurrency and keys, a sandboxed image builder (§3.5).

---

## 17. Testing & evaluation

**Unit** (`-tags exclude_frontend,unit`), in the same style as Pocket ID:
- **Services** are tested against `testutil.NewDatabaseForTest(t)` (all migrations applied) and `NewActorHostForTest`. CI runs the suite twice: on in-memory SQLite, and on Postgres in a service container.
- **Fakes:** the agent loop is tested against `llm/fake` and `sandbox/fake`. The LLM adapters are tested against recorded HTTP fixtures.
- **Also covered:**
  - playbook and Dockerfile op validation
  - cron/timezone edge cases
  - the script header parser
  - job-actor concurrency policies
  - the run-handler redelivery invariant
  - `listquery` whitelists
  - the **tenant-scoping test** over all `queries.sql` files

**Integration** (`-tags integration`, real backends):
- `sandboxtest` against the Docker adapter, including `ImageBuilder`, on Docker and rootless Podman in CI.
- stdio MCP in the sandbox.
- Broker reachability in both deployment modes.

**End-to-end** (`tests/`, Playwright), set up as in Pocket ID:
- **Stack:** `tests/setup/docker-compose.yml` builds `docker/Dockerfile` with `BUILD_TAGS=e2etest` and `APP_ENV=test`, and mounts the Docker socket, so runs use real sandboxes.
- **LLM:** responses come from the `fake` provider, scripted per test via `/api/test/llm-script`.
- **Isolation:** `beforeEach` calls `/api/test/reset`, which wipes and reseeds the database, including Francis state and alarms.
- **Auth:** every test resets the backend and signs in through `/api/test/session`, so OIDC is not needed in tests. `workers: 1`.
- **CI matrix:**
  - **single:** SQLite, filesystem, 1 replica
  - **ha:** Postgres, SeaweedFS (S3), 2 replicas behind a proxy

  HA-only specs cover: exactly-once schedule firing, cross-replica cancel and SSE, and killing a replica mid-run.
- **Specs** cover:
  - create job → compile → run → live timeline
  - schedule firing via `/api/test/jobs/{id}/fire-schedule`
  - Dockerfile build → run uses the image
  - server-side table search, sort, filter and paging, with URL state
  - webhooks, cancel, playbook rollback

**Frontend:** `svelte-check` is the type gate. UI behaviour is covered by the Playwright suite.

**Self-improvement eval set:** 5 reference jobs, each run 6× in sequence, measuring success, turns, cost and duration per run. This is the product's core metric, tracked in CI (nightly, small budget, real models). Suggested jobs:
1. Stale PR digest (GitHub + Slack MCP, side effects)
2. TLS certificate expiry check for a list of domains (pure shell)
3. CSV → cleaned JSON with messy edge cases (data wrangling)
4. "What changed on page X since last run" (web fetch + job state)
5. Weekly dependency-update summary for a repo (judgment step → `ump llm`)

---

## 18. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Docker socket is root-equivalent | Recommend rootless Podman or a socket proxy; document it clearly; gVisor via `SANDBOX_DOCKER_RUNTIME=runsc`. SaaS uses the Kubernetes/microVM adapters instead |
| One sqlc package on two engines hides dialect bugs | Portable-SQL rules, the SQLite `sqlc compile` check, and every service test on both engines. Escape hatch: engine-specific code behind a module interface |
| Someone adds in-memory state or a bare goroutine, breaking HA | §3.4 rules in `AGENTS.md`, and the 2-replica e2e topology in CI |
| Tenant data leaks across workspaces (once SaaS exists) | `workspace_id` on every tenant table, explicit `workspaceID` parameters, and the tenant-scoping query test |
| Image builds are less isolated than runs | Self-host: only admin- or reflection-authored Dockerfiles, build limits, flagged risky patterns. SaaS: a sandboxed rootless builder |
| Adapters diverge in behaviour | One written contract (§4.2), the shared conformance suite, and capability checks at job save and run start |
| The interface leaks Docker assumptions and blocks later adapters | Review it against the Kubernetes and microVM sketches (§4.8) before M1 is locked. Users are roles, there's no tar in the write path, and broker reachability belongs to the adapter |
| Francis is pre-1.0, and HA depends on it | Pocket ID relies on it for the same things. Actor code is confined to `jobs`, `runs`, `images` and `playbook` |
| Durable run task is redelivered after a crash | The run handler only proceeds from `queued`. Interrupted runs are failed and never re-executed (§3.3) |
| Prompt injection poisons learnings or the Dockerfile | Provenance, per-job toggle, flagged patterns, diff UI, rollback (§9.4) |
| Duplicate side effects on fallback | `ump step` markers + broker call log passed to the fallback agent; no shadow runs for side-effecting scripts |
| Providers vary in tool-calling quality | `caps` per model; tool-capable models required for the agent role; JSON Schema validation of tool args, with errors returned as tool results |
| Cost runaway | Per-run turn/cost/time caps, per-workspace daily cap, reflection skipped on successful scripted runs |
| Long runs exceed context | Output truncation to sandbox files, compaction in M6 |
| Playbook bloat | Token budget for the render; reflection must merge/retire; hits tracking |

---

## 19. Open decisions (my default in **bold**)

1. ~~**Egress control in v1**~~: decided, see below.
2. **"History saved anyway" when self-improve is off.** My reading: **all runs and playbook versions are always recorded, reflection doesn't run, and you can learn from any saved run later.** Alternative: reflection still runs and stores *proposals* that aren't applied, which costs money.
3. ~~**Graduation thresholds**~~: decided, see below.
4. **Naming:** "Job" / "Run" / "Playbook" in the UI, or lean into the brand?
5. **Default models:** **`claude-opus-5-5` (agent) + `claude-haiku-4-5` (utility)** seeded on first start, both changeable in Settings.
6. **Who gets in under OIDC:** **anyone the IdP lets through the client**, optionally narrowed by `OIDC_ALLOWED_GROUPS`. Alternative: pin a single `sub`.
7. **Docker binary-mode broker path:** **a shared `umpteenth-relay` container**. Alternative: binary mode supports only `network: internet`, reaching the broker via `host-gateway`.
8. **Reflection and the Dockerfile:** **`set_dockerfile` applies automatically when self-improve is on**, like every other op (it triggers a build, and rollback restores the old image). Alternative: Dockerfile changes are only proposed and need a click.

**Decided on 2026-09-26 for M6:**
- **Egress:** `internet` sandboxes can't reach private ranges, the host or metadata unless a job allows the private network; metadata stays blocked either way. A new `allowlist` mode reaches only listed domains through a proxy.
- **Notifications:** a webhook, which also covers Slack, Discord and automation tools.
- **Docs:** an Astro Starlight site in `docs/` of this repo.
- **Export:** `umpteenth export` carries secret names, never their values.

**Decided on 2026-09-26:**
- **Graduation:** 3 consecutive successful Assisted runs, ≤ 2 ad-hoc bash calls each, same toolkit sequence. Automatic for every job, including ones with side effects.
- **Re-evaluation:** a failed scripted run falls back to Assisted in the same sandbox and reflection repairs `main`. After 2 fallbacks in a row the job is demoted to Assisted and graduates again once the criteria hold again.
- **Verification:** deterministic checks always, the LLM judge only when reflection enables it for the job.

**Decided on 2026-09-25:**
- **Auth:** OIDC only.
- **Sandbox:** one global adapter via env. Future adapter targets are local runtimes and Kubernetes.
- **UI:** English only, with server-side TanStack tables.
- **Background work and HA:** Francis for all async work, HA-ready by construction.
- **Stack:** SQLite + Postgres, sqlc + goose, Huma. The broker stays HTTP.
- **Tenancy:** `workspace_id` from day one.
- **Environments:** per-job Dockerfiles.

---

## 20. Considered & rejected (for now)

| Option | Why not (now) |
|---|---|
| Firecracker microVMs as the **v1** adapter | Linux + KVM only (no macOS dev, many VPSs lack nested virt), OCI→rootfs pipeline. Planned as a later adapter behind `sandbox.Adapter` |
| Hosted sandboxes (E2B, Daytona, Modal) or Anthropic Managed Agents | Not self-hosted or lightweight, vendor lock-in, conflicts with provider-agnostic, and the broker would need to be public. The interface doesn't design for them |
| bubblewrap/nsjail | No per-job images, host toolchain leaks into jobs, Linux-only |
| Per-job sandbox profiles (adapter/runtime chosen per job) | One global adapter via env is simpler for v1. `runs.sandbox_adapter` is recorded, so profiles can be added later without a data migration |
| Broker tunnelled over the sandbox's exec stream | It would remove the network path to the host entirely, but plain HTTP is simpler to build and debug. Reachability is the adapter's job instead (§4.5) |
| Snapshot-based image baking (commit the post-setup container) | Not reproducible, not portable across replicas or adapters, and not reviewable. Dockerfiles are versioned, diffable and rebuildable |
| Running a full agent CLI/SDK (e.g. Claude Code) inside the sandbox | Puts API keys in the sandbox, single-provider, heavy image, and hides the loop we need to control for graduation and cost tracking |
| Postgres only | The simplest path to HA, but SQLite keeps single-node self-hosting free of a database server. Dual support costs one extra migration file per schema change |
| Gin + GORM (Pocket ID's stack) | We keep Pocket ID's structure and its dual-database support, but use sqlc with portable SQL instead of GORM, and Huma to generate the TS client |
| Pocket ID's custom `advanced-table` | TanStack via shadcn-svelte's data-table covers the same server-side model with column visibility, faceted filters and URL state built in |
| Paraglide i18n from day one | UI copy changes too often during M0–M5. Adding it later is one sweep |
| Local admin password | OIDC only keeps auth out of our codebase. Pocket ID or any OIDC provider works |
| Warm sandbox pool | Measured on 2026-09-26: creating a sandbox takes 0.26 s with runc and 0.3–0.4 s with gVisor, and the first command 25–35 ms, against several seconds for a run's first model call. A pool would save under 3% of a typical run while having to create sandboxes without their per-run environment, token, network, image and limits. Worth revisiting for adapters that provision in seconds, such as Kubernetes or microVMs |
