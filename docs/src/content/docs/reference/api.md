---
title: REST API
description: Automate Umpteenth over HTTP with API tokens, from starting runs to reading results and following a run live.
---

The UI does everything through a JSON API under `/api`, and a script with an API token calls the same routes.
Each instance serves its OpenAPI spec at `/api/openapi.json` (or `/api/openapi.yaml`) and interactive docs with every route and schema at `/api/docs`.
Neither needs a sign-in, and [`umpteenth openapi`](../server-cli/#openapi) prints the same spec without a running server.

In the examples, `https://umpteenth.example.com` stands for your instance and `$UMPTEENTH_API_TOKEN` holds an API token.

## API tokens

Create a token under **Settings → API tokens** with **Create token**.
The dialog asks for a **Name** and an optional **Expires** date, and a token without a date never expires.
Umpteenth shows the new token, which starts with `ump_`, once in the **API token created** dialog, so copy it before you click **Done**.

Send it as a bearer token:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" https://umpteenth.example.com/api/jobs
```

Tokens have no scopes.
A token can do anything a signed-in user can across the whole instance, with two exceptions.
Creating or deleting an API token answers `403` with "API tokens can only be managed from a signed-in session", so a leaked token can't mint its own successor.
Starting an OAuth login to an MCP server answers `403` with "Logging in to an MCP server needs a signed-in user, not an API token".

The token list shows a **Last used** time for each token, accurate to the minute, and an **Expired** badge on a token past its date.
Delete a token with its trash icon, and every call that uses it fails from then on.
A wrong, deleted or expired token gets `401` with the code `invalid_token`.

The API accepts secret values and provider API keys but never returns them: secrets come back without their values, and providers report `hasApiKey`.

## Errors

Error answers share one JSON shape:

```json
{
  "code": "validation_failed",
  "message": "sort has unknown sort key \"price\"",
  "fields": [{ "field": "sort", "code": "invalid", "message": "has unknown sort key \"price\"" }],
  "requestId": "9c1f0e4b7a2d3c58"
}
```

| Code | Status | Typical cause |
|---|---|---|
| `validation_failed` | 400 | A field or query parameter failed its checks, listed in `fields` |
| `invalid_request_body` | 400 | The body isn't valid JSON for the route |
| `invalid_token` | 401 | The API token or webhook token is wrong, deleted or expired |
| `not_signed_in` | 401 | The request carried neither a token nor a session |
| `forbidden` | 403 | A token tried to manage API tokens or start an MCP server login |
| `not_found` | 404 | The job, run or other resource doesn't exist |
| `conflict` | 409 | The action doesn't fit the current state, such as cancelling a finished run |
| `rate_limited` | 429 | Too many webhook or login calls; retry after the seconds in the `Retry-After` header |

Every answer carries an `X-Request-ID` header, and error bodies repeat it as `requestId`.
Send your own `X-Request-ID` of up to 64 characters to find a call in the server log.
The server logs each API call that answers `400` or above with its request ID, and the other calls only at the `debug` log level.

## Start a run

`POST /api/jobs/<job ID>/runs` starts a run, like **Run now** in the UI.
The job ID is the part after `/jobs/` in the job page's URL.

```bash
curl -X POST "https://umpteenth.example.com/api/jobs/$JOB_ID/runs" \
  -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"input": {"repo": "acme/api"}, "instructions": "Skip draft pull requests."}'
```

Both fields are optional:

| Field | Effect |
|---|---|
| `input` | Any JSON value, which the run finds in `/ump/input.json`. Without it the file holds `{}`. |
| `instructions` | Up to 10,000 characters that the agent receives as "Additional instructions for this run". A scripted run's main script never sees them, so they reach the agent only if the run [falls back](../../guides/self-improvement/#fallback). |

The answer names the new run and its status:

```json
{ "runId": "0199846e-3b7c-7d2a-9f4e-5c1b2a3d4e6f", "status": "queued" }
```

`queued` covers a run that starts at once and a run that waits behind another one under the **Queue** overlap policy.
`skipped` means another run of the job was active and the job's overlap policy is **Skip**.
Umpteenth records the skipped run with the error "Skipped because another run of this job was still active".
[Triggers and schedules](../../guides/triggers/#overlapping-runs) explains the policies.

These runs show the trigger **API** in the UI.
A paused job accepts them, because the **Enabled** switch only stops schedule and webhook runs.
To pause or resume a job from a script, send `PATCH /api/jobs/<job ID>` with `{"enabled": false}` or `{"enabled": true}`.
An unknown or deleted job answers `404`.

## Read a run

`GET /api/runs/<run ID>` returns a run with its status, cost and results:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" "https://umpteenth.example.com/api/runs/$RUN_ID"
```

The fields you'll reach for first:

| Field | Contents |
|---|---|
| `status` | The run's status, listed below |
| `mode` | `explore`, `assisted` or `scripted` |
| `trigger` | `manual`, `schedule`, `webhook`, `api` or `retry` |
| `summary` | The run's Markdown summary |
| `outputs` | The run's outputs as a JSON object |
| `error` | The reason the run didn't succeed |
| `cost` | The run's cost in micro-USD, so `420000` means $0.42 |
| `turns`, `tokIn`, `tokOut` | The agent's turns, and the input and output tokens of the run's model calls, `ump llm` included |
| `queuedAt`, `startedAt`, `finishedAt` | Unix timestamps in milliseconds |
| `msTotal` | Milliseconds from leaving the queue to the end of the run |
| `fellBack` | `true` when a scripted run handed over to the agent |
| `reflection` | `skipped`, `pending`, `done` or `failed` |
| `reflectionCost`, `verifyCost` | The cost of learning from the run and of verifying a scripted run, in micro-USD |

A run is live while its status is `queued`, `provisioning`, `running` or `verifying`.
It is final once it reads `succeeded`, `failed`, `timed_out`, `cancelled` or `skipped`.
[Reading a run](../../guides/runs/#run-statuses) explains each status.

To wait for the result in a script, poll until the status is final:

```bash
while :; do
  status=$(curl -fsS -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" \
    "https://umpteenth.example.com/api/runs/$RUN_ID" | jq -r .status)
  case "$status" in
    queued|provisioning|running|verifying) sleep 10 ;;
    *) echo "$status"; break ;;
  esac
done
```

## Timeline and live stream

`GET /api/runs/<run ID>/events` returns the run's timeline as a JSON array, oldest first.
Each event has a `seq`, a `ts` in Unix milliseconds, a `type`, `spanId`, `parentSpanId`, `ms` and a `payload` whose shape depends on the type.
The route returns up to `limit` events (500 by default, 2,000 at most) after the sequence number in `after`, so pass the last `seq` you got to read the next batch.

`GET /api/runs/<run ID>/stream` sends the same timeline as server-sent events: it replays the stored events, then follows the run live.

```bash
curl -N -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" "https://umpteenth.example.com/api/runs/$RUN_ID/stream"
```

| Event | Data |
|---|---|
| `event` | A timeline entry as `/events` returns it, with its `seq` as the SSE ID |
| `delta` | A live fragment of the model's output or a command's output, which Umpteenth doesn't store |
| `status` | The run's new status |
| `end` | The final status, after which Umpteenth closes the stream |

To resume after a dropped connection, send the last SSE ID as `Last-Event-ID` or as `?after=<seq>`.
Umpteenth sends a keepalive comment every 20 seconds.

`GET /api/events` streams a `run` message for each status change of any run or its reflection on the instance, with `kind` (`run` or `reflection`), `runId`, `jobId` and `status`.
It starts at the moment you connect and replays nothing.

Behind a reverse proxy, turn response buffering off for both streams, as [Reverse proxy](../../deployment/reverse-proxy/#live-updates) shows.

## Artifacts

`GET /api/runs/<run ID>/artifacts` lists the files the run left in `/ump/outputs`:

```json
[{ "name": "digest.md", "size": 2048 }, { "name": "reports/week.csv", "size": 31544 }]
```

A file from a subdirectory keeps its relative path as its name.
`GET /api/runs/<run ID>/artifact?path=<name>` downloads one:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" -o digest.md \
  "https://umpteenth.example.com/api/runs/$RUN_ID/artifact?path=digest.md"
```

## Cancel, retry and learn

The run page's **Cancel**, **Retry** and **Learn from this run** buttons call these routes:

| Route | Effect |
|---|---|
| `POST /api/runs/<run ID>/cancel` | Stops a live run, which ends as `cancelled`. A queued run ends at once with "Cancelled before it started". A finished run answers `409` with "The run is not running". |
| `POST /api/runs/<run ID>/retry` | Starts a new run of the same job with the same `input` and `instructions` and the trigger `retry`. The answer has the shape of [Start a run](#start-a-run). |
| `POST /api/runs/<run ID>/learn` | Starts reflection on a run that succeeded, failed or timed out and answers `202` with `{"reflection": "pending"}`. Any other run, and a run with reflection in progress, answers `409`. |

[Reading a run](../../guides/runs/#cancel-retry-and-learn) describes the same actions in the UI.

## Webhooks

A job's webhook, `POST /hooks/<job ID>`, lives outside `/api` and takes the job's own webhook token instead of an API token.
`POST /api/jobs/<job ID>/webhook-token` creates that token or replaces the current one, the same as **Generate token** and **Rotate token** in the job's **Webhook** card.
The answer holds the token, which starts with `umh_`, and the webhook's path:

```json
{ "token": "umh_…", "url": "/hooks/01998412-9a5d-7e01-b3c6-2f8e7d6a5b40" }
```

Umpteenth returns the token in this answer alone, and the previous token stops working at once.
[Triggers and schedules](../../guides/triggers/#webhooks) covers the request body, the answers and the rate limit.

## Lists

List routes share four query parameters and answer with the same envelope:

| Parameter | Default | Notes |
|---|---|---|
| `page` | `1` | Counts from 1 |
| `pageSize` | `25` | 1 to 100; a larger value answers `400` |
| `sort` | Per route | Up to three comma-separated keys, each with a `-` in front for descending, such as `-cost,number` |
| `search` | None | Free text, up to 200 characters |

```json
{ "items": [], "page": 1, "pageSize": 25, "total": 0 }
```

Filters take comma-separated values.
The two lists you'll script most:

| | `GET /api/runs` | `GET /api/jobs` |
|---|---|---|
| Filters | `status`, `job` (job IDs), `mode`, `trigger`, `from` and `to` (queued time in Unix milliseconds, `to` exclusive) | `enabled` (`true` or `false`) |
| Sort keys | `queuedAt` (default `-queuedAt`), `startedAt`, `finishedAt`, `duration`, `cost`, `turns`, `status`, `job`, `number`, `tokens` | `name` (default), `createdAt`, `updatedAt`, `nextRunAt`, `enabled`, `runCount` |
| `search` matches | Summary, error and job name | Name and instruction |

The ten most expensive failed or timed-out runs:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" \
  "https://umpteenth.example.com/api/runs?status=failed,timed_out&sort=-cost&pageSize=10"
```

## Other routes

`/api/docs` documents each of these with its fields.

| Area | Routes |
|---|---|
| Jobs | `GET` and `POST /api/jobs`, `GET`, `PATCH` and `DELETE /api/jobs/{id}` (`DELETE` archives the job, keeps its runs and cancels its queued ones), `POST /api/jobs/compile` |
| Job state | `GET /api/jobs/{id}/state`, `GET`, `PUT` and `DELETE /api/jobs/{id}/state/{key}` |
| Playbook | `GET` and `PUT /api/jobs/{id}/playbook`, `GET /api/jobs/{id}/playbook/versions` and `/versions/{version}`, `POST /api/jobs/{id}/playbook/rollback` |
| Job images | `GET /api/jobs/{id}/images`, `POST /api/jobs/{id}/images/rebuild` |
| Job secrets and MCP servers | `GET` and `PUT /api/jobs/{id}/secrets` and `/api/jobs/{id}/mcp-servers` |
| Secrets | `/api/secrets` and `/api/secrets/{id}` |
| Providers and models | `/api/providers`, `/api/models` and `/api/catalog/models` |
| MCP servers | `/api/mcp-servers` and `/api/mcp-servers/{id}` |
| Settings | `GET` and `PATCH /api/settings`, `POST /api/settings/notifications/test` |
| Stats | `GET /api/stats/overview`, `GET /api/jobs/{id}/stats` |
| System | `GET /api/system/info` with the version, the database and the sandbox backend |
