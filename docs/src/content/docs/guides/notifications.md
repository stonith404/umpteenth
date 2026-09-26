---
title: Notifications
description: Get a Slack, Discord or webhook message when a run fails, a script falls back to the agent or Umpteenth demotes a job.
---

Umpteenth posts a JSON message to a webhook when a run needs your attention, so you hear about a failed nightly job before someone asks where its report went.
Slack and Discord incoming webhooks take the message as it is, and anything else that accepts a JSON `POST` can read the structured fields.

## Set it up

1. Create an incoming webhook for a Slack or Discord channel and copy its URL.
2. Open **Settings → General** and paste the URL into **Webhook URL** in the **Notifications** card.
3. Tick the events you want under **Notify when** and click **Save** in the unsaved-changes bar.
4. Click **Send test** in the card's header.

**Send test** posts to the saved URL, so the button stays disabled until you save one.
Umpteenth shows "Sent a test notification" if the receiver accepted it, and otherwise "The webhook did not accept the notification:" followed by the receiver's error.
The test message has the event `test` and the text `Umpteenth: this is a test notification. Failed runs will be reported here.`, without `job` or `run` fields.
Umpteenth sends it once, without retries.

Clear **Webhook URL** and save to turn notifications off.

## Events

| Checkbox under **Notify when** | Event | Default |
|---|---|---|
| A run fails or times out | `run.failed` | On |
| A scripted run's main script fails and the agent takes over | `run.fell_back` | Off |
| A job is demoted to Assisted after two fallbacks in a row | `job.demoted` | On |

`run.failed` covers runs that end as **Failed** or **Timed out**, including runs Umpteenth marks as interrupted because their replica stopped responding.
Umpteenth sends `run.fell_back` whether or not the agent then finishes the job, and sends `run.failed` as well if the run then fails or times out.
`job.demoted` arrives together with a fallback, once the last two scripted runs of a graduated job both fell back.
See [How jobs learn](../self-improvement/) for fallbacks and demotion.

Umpteenth sends nothing for skipped runs, or for successful and cancelled runs that didn't fall back.

## Payload

Every notification is a `POST` with a JSON body like this one, from a run that hit its time limit:

```json
{
  "event": "run.failed",
  "text": "Umpteenth: Stale PR digest run #12 timed out: the run exceeded its time limit\nhttps://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10",
  "content": "Umpteenth: Stale PR digest run #12 timed out: the run exceeded its time limit\nhttps://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10",
  "job": { "id": "019e87c6-9900-7a4b-9d2e-6f1a3c8b2e47", "name": "Stale PR digest" },
  "run": {
    "id": "01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10",
    "number": 12,
    "status": "timed_out",
    "mode": "assisted",
    "trigger": "schedule",
    "fellBack": false,
    "error": "the run exceeded its time limit",
    "url": "https://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10"
  },
  "timestamp": "2026-09-26T08:15:04Z"
}
```

`text` and `content` carry the same message, because Slack reads `text` and Discord reads `content`.
The text depends on the event and ends with a newline and the link to the run:

| Event | Text |
|---|---|
| `run.failed` | `Umpteenth: <job> run #<n> failed: <error>`, or `timed out` in place of `failed` |
| `run.fell_back` | `Umpteenth: <job> run #<n>: the main script failed and the agent finished the job` |
| `job.demoted` | `Umpteenth: <job> was demoted to Assisted after its main script failed twice in a row` |

The fields of `run`:

- `status` is `failed` or `timed_out` for `run.failed`, and the run's final status for the other events.
- `mode` is `explore`, `assisted` or `scripted`.
- `trigger` is `manual`, `schedule`, `webhook`, `api` or `retry`.
- `error` holds the run's error and is missing if the run has none.
- `url` is `<app.url>/runs/<run id>`, with `app.url` from the config file.

`timestamp` is the time Umpteenth sent the message, in UTC.

Each request carries these headers:

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `Umpteenth` |
| `X-Umpteenth-Event` | The event, such as `run.failed` or `test` |
| `X-Umpteenth-Signature` | `sha256=<hex>`, if you picked a signing secret |

## Delivery and retries

Umpteenth queues each notification as a background task, so a restart doesn't lose it.
It waits up to 10 seconds for each answer and counts any `2xx` status as delivered.

After a `5xx` status, a `429`, a timeout or a connection error, Umpteenth tries again, up to six attempts in total.
The pauses between attempts grow from about two seconds to about ten, so a receiver that stays down for more than about 20 seconds misses the message.
Any other status, such as `404`, ends delivery on the spot, and Umpteenth logs "The notification webhook rejected a delivery".

## Signatures

Pick a secret from **Settings → Secrets** under **Signing secret** to sign every message, test messages included.
The select starts at **Unsigned**.

A signed request carries `X-Umpteenth-Signature` with `sha256=` and the hex HMAC-SHA256 of the raw request body, keyed with the secret's value.
GitHub's `X-Hub-Signature-256` uses the same scheme, so verification code written for GitHub webhooks works once you point it at this header.
Check the signature against the raw bytes before you parse the JSON:

```python
import hashlib, hmac

def valid(body: bytes, header: str, secret: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

If you delete the secret that **Signing secret** points to, Umpteenth drops every delivery with a log line, and **Send test** shows the error.
Pick another secret or **Unsigned**.

## Webhooks on private addresses

Umpteenth posts to a webhook URL on your LAN unless you set `network.allow_private_targets` to `false` in the config file.
With that setting, saving a URL that resolves to a private or local address fails with "points to a private or local network address", and Umpteenth checks the address again on every delivery.
[Configuration](../../deployment/configuration/#network) lists the option, and [Security](../../deployment/security/#umpteenths-own-outbound-calls) covers the other outbound calls it controls.
