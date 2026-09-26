---
title: REST API
description: Automate Umpteenth over HTTP with an API token, from starting a run and waiting for its result to following it live and downloading its files.
---

The UI does everything through a JSON API under `/api`, and a script with an API token calls the same routes.
Your instance serves the OpenAPI spec at `/api/openapi.json` and interactive docs at `/api/docs`, and neither needs a sign-in.
[API endpoints](../api-endpoints/) lists every route on one page with its parameters and response fields.

In the examples, `https://umpteenth.example.com` stands for your instance and `$UMPTEENTH_API_TOKEN` holds an API token.

## API tokens

Create a token under **Settings → API tokens** with **Create API token**.
The dialog asks for a **Name** and an optional **Expires** date, and a token without a date never expires.
Umpteenth shows the new token, which starts with `ump_`, once in the **API token created** dialog, so copy it before you click **Done**.

Send it as a bearer token:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" https://umpteenth.example.com/api/jobs
```

A token works in the workspace you created it in and acts with your [role](../../guides/workspaces/#roles) there, so a member's token can run jobs and can't change providers.
It stops working once you leave the workspace or an admin removes or deactivates you.
Creating and deleting tokens, managing members, invites and workspaces, the admin area and OAuth logins to MCP servers all need a signed-in session, and those routes answer a token with `403`.

To revoke a token, delete it from its row's menu in the token list.
A wrong, deleted or expired token gets `401` with the code `invalid_token`.

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
| `forbidden` | 403 | Your role doesn't allow the route, or a token called a route that needs a signed-in session |
| `not_found` | 404 | The job, run or other resource doesn't exist |
| `conflict` | 409 | The action doesn't fit the current state, such as cancelling a finished run |
| `rate_limited` | 429 | Too many webhook or login calls; retry after the seconds in the `Retry-After` header |

Every answer carries an `X-Request-ID` header, and error bodies repeat it as `requestId`.
Search the server log for that ID to find the failed call.

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
| `input` | Any JSON value, which the run reads from `/ump/input.json`. Without it the file holds `{}`. |
| `instructions` | Up to 10,000 characters of extra guidance for the agent. The main script of a scripted run never receives them. |

The keys a job expects in `input` are its declared inputs, which the **Spec** card on the job's **Overview** tab lists under **Inputs**.
**Run now** prefills the same keys as a JSON template, so you can copy them from there.

The answer names the new run and its status:

```json
{ "runId": "0199846e-3b7c-7d2a-9f4e-5c1b2a3d4e6f", "status": "queued" }
```

`queued` covers a run that starts at once and one that waits behind another run of the job.
`skipped` means another run was still active and the job's overlap policy is **Skip**, as [Triggers and schedules](../../guides/triggers/#overlapping-runs) explains.

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
| `queuedAt`, `startedAt`, `finishedAt` | Unix timestamps in milliseconds |

A run is live while its status is `queued`, `provisioning`, `running` or `verifying`.
It is final once it reads `succeeded`, `failed`, `timed_out`, `cancelled` or `skipped`, and [Reading a run](../../guides/runs/#run-statuses) explains each one.

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

`GET /api/runs` lists runs, newest first and 25 to a page.
It filters by `status`, `mode`, `trigger` and `job` (job IDs), each taking comma-separated values, and `sort` takes keys such as `cost` or `queuedAt` with a `-` in front for descending.
The ten most expensive failed or timed-out runs:

```bash
curl -H "Authorization: Bearer $UMPTEENTH_API_TOKEN" \
  "https://umpteenth.example.com/api/runs?status=failed,timed_out&sort=-cost&pageSize=10"
```

[API endpoints](../api-endpoints/#list-runs) lists the other parameters.

## Timeline and live stream

`GET /api/runs/<run ID>/events` returns the run's timeline as a JSON array, oldest first.
Each event has a sequence number `seq` and a `payload` whose shape depends on its `type`.
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

`GET /api/events` streams a `run` message whenever a run or its reflection in your workspace changes status, with `kind` (`run` or `reflection`), `runId`, `jobId` and the new `status`.
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

The run page's **Stop**, **Retry** and **Learn from this run** buttons call these routes:

| Route | Effect |
|---|---|
| `POST /api/runs/<run ID>/cancel` | Stops a queued or live run, which ends as `cancelled`. A finished run answers `409`. |
| `POST /api/runs/<run ID>/retry` | Starts a new run of the same job with the same `input` and `instructions` and the trigger `retry`, and answers like [Start a run](#start-a-run). |
| `POST /api/runs/<run ID>/learn` | Starts reflection on a run that succeeded, failed or timed out, and answers `202`. Any other run, and a run whose reflection is in progress, answers `409`. |

## Webhooks

A job's webhook, `POST /hooks/<job ID>`, lives outside `/api` and takes the job's own webhook token instead of an API token.
`POST /api/jobs/<job ID>/webhook-token` creates that token or replaces the current one, like **Generate token** and **Rotate token** in the job's **Webhook** card, and answers with the token and the webhook's path:

```json
{ "token": "umh_…", "url": "/hooks/01998412-9a5d-7e01-b3c6-2f8e7d6a5b40" }
```

Umpteenth returns the token in this answer alone, and the previous token stops working at once.
[Triggers and schedules](../../guides/triggers/#webhooks) covers the request body, the answers and the rate limit.
