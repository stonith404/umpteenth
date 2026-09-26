---
title: Job settings
description: Every field on a job's Settings and Environment tabs, with the value a new job starts with and what it changes.
---

Look up a field on a job's **Settings** or **Environment** tab, with the value a new job starts with and what the field changes.
The sections follow the cards in the order the app shows them, and the Default column assumes a fresh instance.

Your edits on the **Settings** tab stay in the **Unsaved changes** bar until you click **Save** or press ⌘S (Ctrl+S on Windows and Linux).
The webhook token is the exception: it changes as soon as you generate or rotate it.
Each run reads the job's settings when it starts, so a change applies from the next run on.
The job's header repeats the **Enabled** switch next to **Run now**, and that switch saves at once.

## General

[Models and costs](../../guides/models/) explains the three model roles, and [Managing jobs](../../guides/jobs/#editing-a-job) covers what an instruction edit leaves alone.

| Field | Default | Effect |
|---|---|---|
| **Name** | From the **New job** page | The job's name in lists and on run pages, up to 200 characters |
| **Instruction** | From the **New job** page | The text the agent follows on every run, up to 20,000 characters, Markdown allowed. Editing it leaves the compiled spec as it was. |
| **Model** | **Workspace default**, followed by the Agent model's name | The model that runs the agent for this job. The list offers enabled models only, and if someone disables the model later, the job falls back to the workspace's Agent model. |

## Schedule

[Triggers and schedules](../../guides/triggers/#schedules) explains cron syntax and time zones, and its [overlapping runs](../../guides/triggers/#overlapping-runs) section compares the three policies.

| Field | Default | Effect |
|---|---|---|
| **Enabled** | On | Off pauses the job: its schedule and webhook start no runs, while **Run now**, the API and **Retry** keep working. |
| **Run on a schedule** | On when the job has a schedule | Off leaves the job to **Run now**, its webhook and the API. Switching it on fills in `0 9 * * *` and your browser's time zone. |
| **Cron expression** | From the compile step | Five fields (minute, hour, day of month, month, day of week) or a descriptor such as `@daily`, without a seconds field. **Presets** offers five common schedules. |
| **Timezone** | From the compile step, `UTC` when empty | The IANA time zone Umpteenth evaluates the expression in, such as `Europe/Berlin` |
| **When runs overlap** | **Skip** | **Skip** records a trigger during an active run as a skipped run. **Queue** makes it wait for the active run to finish, and **Parallel** starts a run right away. |

## Sandbox

Empty limit fields follow **Settings → General → Sandbox defaults**, and their placeholders show the current workspace values.
[Sandboxes](../../guides/sandboxes/) explains the image and network options, and [Managing jobs](../../guides/jobs/#limits-per-run) says what happens when a run hits a limit.

| Field | Default | Effect |
|---|---|---|
| **Base image** | The workspace's **Default image** | The image runs start from while the job has no Dockerfile. A Dockerfile's `FROM` line takes its place. |
| **Network** | From the **New job** page, where the compile step picks **Internet access** or **No network** | **Internet access**, **Allowed domains only** or **No network** |
| **Run as root** | Off | Runs the agent's commands and the playbook's scripts as root, so the agent can install system packages during a run |
| **Allowed domains** | Empty | Appears for **Allowed domains only**. One host name per line, 1 to 100 of them, `*.example.com` for every name below `example.com`, with no scheme, port, path or IP address. |
| **Allow private network** | Off | Appears for **Internet access** and **Allowed domains only**. Lets the job reach the LAN and the Docker host, while cloud metadata stays blocked. |
| **Timeout** | 900 seconds | 30 to 86,400 seconds for the whole run, including a wait for the job's image. A run that exceeds it ends as **Timed out**. |
| **Max turns** | 60 | 1 to 1,000 model calls of the agent. A run that exceeds it ends as **Failed**. |
| **Max cost per run** | 2 USD | `0` turns the limit off. A run that exceeds it ends as **Failed**. |
| **CPUs** | 1 | 0.1 to 64 |
| **Memory** | 1024 MB | 64 to 262,144 MB, without swap |
| **Process limit** | 256 | 16 to 65,536 processes |

## Learning

| Field | Default | Effect |
|---|---|---|
| **Self-improve** | On | After runs, reflection writes what worked into a new playbook version that applies without your review, apart from risky changes it holds back. Off stops reflection after runs, so the playbook changes only through your edits and **Learn from this run**. |
| **Mode** | **Automatic** | **Automatic** lets the job graduate as it learns. **Explore**, **Assisted** and **Scripted** pin its runs to that mode, with the exceptions [Managing jobs](../../guides/jobs/#mode-and-learning) lists. |

See [How jobs learn](../../guides/self-improvement/) for reflection and graduation, and its [Held for review](../../guides/self-improvement/#held-for-review) section to apply a change reflection held back.

## MCP servers

You add and test servers on the **MCP Servers** page first, as [MCP servers](../../guides/mcp/) describes.

| Control | Default | Effect |
|---|---|---|
| **Attach a server…** | No servers attached | Gives the job the tools of a server from the **MCP Servers** page. The × on a row detaches it again. |
| Tools button | **All tools** | Limits the job to the tools you tick, and shows a count such as "3 of 12 tools". **All tools** includes tools the server adds later. You can pick tools once you have tested the server on the **MCP Servers** page. |

A **Disabled** badge marks a server that someone switched off on the **MCP Servers** page, and jobs skip it.

## Secrets

Secret values reach the commands of a run as environment variables, and [Sandboxes](../../guides/sandboxes/#secrets) covers what the timeline shows of them.

| Control | Default | Effect |
|---|---|---|
| **Add secret** | No secrets | Adds a row that maps a secret to an environment variable |
| Secret | **Pick a secret** | One of the secrets under **Settings → Secrets** |
| Variable name | The app suggests one from the secret's name, `github-token` becomes `GITHUB_TOKEN` | Letters, digits and underscores, not starting with a digit, and each name once per job |

## Webhook

[Triggers and schedules](../../guides/triggers/#webhooks) shows a full webhook call and its limits.

| Control | Default | Effect |
|---|---|---|
| **URL** | `/hooks/<job ID>` on the address in your browser | Read-only, with a copy button |
| **Generate token** or **Rotate token** | **No token** | Creates the token callers send as `Authorization: Bearer <token>` and shows it once in the **Webhook token** dialog. Rotating stops the old token right away. |

The webhook refuses every call until you generate a token.

## Delete job

**Delete job** stops the schedule and the webhook and cancels the job's queued runs.
Active runs finish, and past runs and their outputs stay in the run history.
The app has no way to bring a deleted job back, so pause a job you might want later ([Managing jobs](../../guides/jobs/#deleting-a-job)).

## Environment tab

The **Dockerfile** editor starts empty unless the compile step proposed a Dockerfile, and runs use the **Base image** while it stays empty.
[Sandboxes](../../guides/sandboxes/#a-dockerfile-per-job) has the rules for Dockerfiles and their builds.

| Control | Effect |
|---|---|
| **Start from a template** | Appears while the editor is empty. Fills in a `FROM` line with the workspace's default image and an example `apt-get install` line. |
| **Save & build** | Saves the Dockerfile as a new playbook version and starts a build. Runs wait for the image. |
| **Discard** | Drops unsaved edits |
| **Use the base image** | Removes the Dockerfile after a confirmation. The playbook's history keeps it for a rollback. |
| **Rebuild** | Builds the current Dockerfile again and picks up updates of its base image. Works once the job has a Dockerfile. |

The **Builds** table lists each build with its **Status** (**Queued**, **Building**, **Ready** or **Failed**), **Created**, **Build time**, **Size**, **Digest** and a **Log** button.
If a Dockerfile has no successful build and its latest build failed, runs fail with "Environment build failed" until you fix the Dockerfile or click **Rebuild**.
