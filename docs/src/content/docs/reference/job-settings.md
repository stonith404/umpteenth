---
title: Job settings
description: Every field on a job's Settings tab, with the value a new job starts with, the values it accepts and what it changes.
---

Look up a field on a job's **Settings** tab, with the value a new job starts with and what the field changes.
Each card saves its own fields with its **Save** button, and leaving the page drops the edits you haven't saved.
A saved change applies from the next run on.

## General

| Field | Default | Effect |
|---|---|---|
| **Name** | From the **New job** page | The job's name in lists and on run pages, up to 200 characters |
| **Instruction** | From the **New job** page | The text the agent follows on every run, up to 20,000 characters |
| **Rebuild the spec** | On | Appears once you change the instruction. On compiles the spec again from the new instruction when you save, and Off keeps the spec as it is. |
| **Model** | **Workspace default**, which follows the **Agent** default model | The model that drives the agent for this job, from the enabled models ([Managing jobs](../../guides/jobs/#the-model)) |

## Spec

[Writing instructions](../../guides/writing-instructions/#from-description-to-spec) explains how the compile step fills these fields.

| Field | Default | Effect |
|---|---|---|
| **Goal** | From the compile step | One sentence on the outcome, shown on the job's **Overview** tab. Optional, up to 2,000 characters. |
| **Success criteria** | From the compile step | Statements a successful run satisfies, which the agent reads with the instruction |
| **Inputs** | From the compile step | Named, typed values a run reads from `/ump/input.json` |
| **Outputs** | From the compile step | Named, typed values each run reports |
| **Side effects** | From the compile step | The changes the job makes outside its sandbox, such as a Slack post |

## Schedule

[Triggers and schedules](../../guides/triggers/#schedules) explains cron syntax, time zones and overlapping runs.

| Field | Default | Effect |
|---|---|---|
| **Run on a schedule** | On when the job has a schedule | Off leaves the job to **Run now**, its webhook and the API |
| **Cron expression** | From the compile step | Five fields (minute, hour, day of month, month, day of week) or a descriptor such as `@daily`, without a seconds field. **Presets** offers common schedules. |
| **Timezone** | From the compile step, `UTC` when empty | The IANA time zone Umpteenth evaluates the expression in, such as `Europe/Berlin` |
| **When runs overlap** | **Skip** | **Skip** records a trigger during an active run as a skipped run, **Queue** starts it once the active run finishes, and **Parallel** starts it right away |

## Sandbox

An empty limit field uses the workspace's value under **Settings → General → Sandbox defaults**, which its placeholder shows.
[Managing jobs](../../guides/jobs/#limits-per-run) describes what happens when a run reaches a limit.

| Field | Default | Effect |
|---|---|---|
| **Base image** | Empty, for the workspace's **Default image** | The image runs start from while the job has no Dockerfile |
| **Network** | From the **New job** page | **Internet access**, **Allowed domains only**, **No network**, or **Unrestricted network** while `sandbox.allow_unrestricted_network` is on ([Sandboxes](../../guides/sandboxes/#network)) |
| **Allowed domains** | Empty | Appears for **Allowed domains only**. One host name per line, 1 to 100 of them, with `*.example.com` for every name below `example.com`, and no scheme, port, path or IP address. |
| **Allow private network** | Off | Appears for **Internet access** and **Allowed domains only**. Lets the job reach the LAN and the Docker host, while cloud metadata stays blocked. |
| **Run as root** | Off | Runs the sandbox's commands and scripts as root, so the agent can install system packages |
| **Timeout** | 900 seconds | 30 to 86,400 seconds for the whole run, including a wait for the job's image |
| **Max turns** | 60 | 1 to 1,000 model calls of the agent |
| **Max cost per run** | 2 USD | `0` turns the limit off |
| **CPUs** | 1 | 0.1 to 64 |
| **Memory** | 1024 MB | 64 to 262,144 MB, without swap |

## Learning

Reflection and graduation have their own page, [How jobs learn](../../guides/self-improvement/).

| Field | Default | Effect |
|---|---|---|
| **Self-improve** | On | After runs, reflection turns what worked into new playbook versions, which go live right away. Off stops reflection after runs, along with its model calls. |
| **Graduate to a script** | On | Lets reflection write a main script that replaces the agent once runs repeat. Off keeps every run on the agent with the playbook. |

## MCP servers

You add and test servers on the **MCP servers** page first ([MCP servers](../../guides/mcp/)).

| Control | Default | Effect |
|---|---|---|
| **Attach server** | No servers attached | Picks a server from the **MCP servers** page, whose tools the job gets once you save the card. The × on a server's row detaches it. |
| Tools button | **All tools** | Limits the job to the tools you tick, with a count such as **3 of 12 tools**. The list appears once you have tested the server. |

## Skills

You upload skills on the **Skills** page first ([Skills](../../guides/skills/)).

| Control | Default | Effect |
|---|---|---|
| **Attach skill** | No skills attached | Picks a skill from the **Skills** page, which the job's runs find in `/ump/skills/<name>` once you save the card. The × on a skill's row detaches it. A job takes up to 20 skills with 64 MiB of files in total. |

## Secrets

Each row hands one secret to the job's runs as an environment variable ([Sandboxes](../../guides/sandboxes/#secrets)).

| Control | Default | Effect |
|---|---|---|
| **Add secret** | No secrets | Adds a row that maps a secret to an environment variable |
| **Secret** | **Pick a secret** | One of the secrets under **Settings → Secrets** |
| **Environment variable** | Suggested from the secret's name, so `github-token` becomes `GITHUB_TOKEN` | Letters, digits and underscores, not starting with a digit, and each name once per job |

## Webhook

[Triggers and schedules](../../guides/triggers/#webhooks) shows a full webhook call.

| Control | Default | Effect |
|---|---|---|
| **URL** | `/hooks/<job ID>` on the address in your browser | Read-only, with a copy button |
| **Generate token** or **Rotate token** | **No token** | Creates the token callers send as `Authorization: Bearer <token>` and shows it once. The webhook refuses every call until you generate one, and rotating stops the old token right away. |

## Danger zone

**Delete job** stops the schedule and the webhook and cancels the job's queued runs.
Active runs finish, and past runs keep their outputs in the run history.
Umpteenth can't restore a deleted job, so [stop its automatic runs](../../guides/jobs/#stopping-automatic-runs) instead if you might want it back.

## Environment tab

The **Environment** tab holds the job's Dockerfile, which builds an image with the tools the job needs.
Once the job has one, its runs start from that image, and the Dockerfile's `FROM` line takes the place of the **Base image**.
[Sandboxes](../../guides/sandboxes/#a-dockerfile-per-job) covers writing the Dockerfile, its builds and failed builds.
