---
title: Notifications
seoTitle: Slack, Discord and webhook notifications
description: Get a Slack, Discord or webhook message when a run fails, a script falls back to the agent or Umpteenth demotes a job.
---

Umpteenth posts a JSON message to a webhook when a run needs your attention, so you hear about a failed nightly job before someone asks where its report went.
Slack and Discord incoming webhooks take the message as it is, and anything else that accepts a JSON `POST` can read the structured fields.

## Set it up

Each workspace has its own webhook, which its admins set up:

1. Create an incoming webhook for a Slack or Discord channel and copy its URL.
2. Open **Settings → General** and paste the URL into **Webhook URL** in the **Notifications** card.
3. Tick the events you want under **Notify when** and click **Save** at the bottom of the card.
4. Click **Send test** in the card's header.

If the test fails, Umpteenth shows the receiver's HTTP status, such as `404 Not Found`, or the connection error.
A receiver on your LAN works as long as `network.allow_private_targets` keeps its default ([Security](../../deployment/security/#umpteenths-own-outbound-calls)).
To turn notifications off, clear **Webhook URL** and save.

## Events

| Checkbox under **Notify when** | Event | Default |
|---|---|---|
| A run fails or times out | `run.failed` | On |
| A scripted run's main script fails and the agent takes over | `run.fell_back` | Off |
| A job is demoted to Assisted after two fallbacks in a row | `job.demoted` | On |

`run.failed` covers runs that end as **Failed** or **Timed out**.
A fallback whose run then fails as well sends both `run.fell_back` and `run.failed`.
The page on [how jobs learn](../self-improvement/) explains fallbacks and demotion.

## Payload

Every notification is a `POST` with a JSON body like this one, from a run that hit its time limit:

```json
{
  "event": "run.failed",
  "text": "Umpteenth: Stale PR digest run #12 timed out: the run exceeded its time limit\nhttps://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10?workspace=019e5f21-3b40-7c8d-a1e2-5f6a7b8c9d0e",
  "content": "Umpteenth: Stale PR digest run #12 timed out: the run exceeded its time limit\nhttps://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10?workspace=019e5f21-3b40-7c8d-a1e2-5f6a7b8c9d0e",
  "allowed_mentions": { "parse": [] },
  "job": { "id": "019e87c6-9900-7a4b-9d2e-6f1a3c8b2e47", "name": "Stale PR digest" },
  "run": {
    "id": "01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10",
    "number": 12,
    "status": "timed_out",
    "mode": "assisted",
    "trigger": "schedule",
    "fellBack": false,
    "error": "the run exceeded its time limit",
    "url": "https://umpteenth.example.com/runs/01a0dcba-6c00-7e3a-8b1f-4c2d9e7a5b10?workspace=019e5f21-3b40-7c8d-a1e2-5f6a7b8c9d0e"
  },
  "timestamp": "2026-09-26T08:15:04Z"
}
```

Slack reads `text`, which escapes `&`, `<` and `>`, and Discord reads `content`, the same message as plain text.
Both end with the link to the run, which switches you to the run's workspace when you open it, and `allowed_mentions` keeps Discord from pinging anyone.

The `run` fields name the run's final status, its `mode` (`explore`, `assisted` or `scripted`) and its `trigger` (`manual`, `schedule`, `webhook`, `api` or `retry`).
Each request carries the event in the `X-Umpteenth-Event` header as well.

## Delivery and retries

Umpteenth waits up to 10 seconds for an answer and counts any `2xx` status as delivered.
After a `5xx` status, a `429`, a timeout or a connection error, it tries again for about 20 seconds, so a receiver that stays down longer misses the message.
Any other status, such as `404`, ends delivery on the spot, and Umpteenth logs "The notification webhook rejected a delivery".

## Signatures

To sign every message, pick a secret from **Settings → Secrets** under **Signing secret**.
A signed request carries `X-Umpteenth-Signature` with `sha256=` and the hex HMAC-SHA256 of the raw request body, keyed with the secret's value.
Check the signature against the raw bytes before you parse the JSON:

```python
import hashlib, hmac

def valid(body: bytes, header: str, secret: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

If you delete the secret that **Signing secret** points to, Umpteenth drops every delivery with a log line until you pick another secret or **Unsigned**.
