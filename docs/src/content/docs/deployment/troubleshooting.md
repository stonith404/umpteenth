---
title: Troubleshooting
description: Error messages and symptoms you can hit when you run Umpteenth, with their causes and fixes.
---

Look up the error message or symptom you see to find its cause and the fix.
Startup errors land in the container log, which `docker compose logs umpteenth` shows.

## `localhost` inside the container

Umpteenth runs in a container, and inside it `localhost` and `127.0.0.1` mean the container itself.
A `localhost` URL that works in your browser on the Docker host fails in Umpteenth for any service on the host: an identity provider, Ollama or LM Studio, an MCP server or a notification receiver.

Give Umpteenth an address that leads to the host:

1. Let the service on the host listen on more than `127.0.0.1`, for example on `0.0.0.0`.
2. On Linux, add `extra_hosts: ["host.docker.internal:host-gateway"]` to the `umpteenth` service in `docker-compose.yml` and run `docker compose up -d`.
3. Use `http://host.docker.internal:<port>` in Umpteenth, or the host's LAN address.

[Models and costs](../../guides/models/#a-model-server-on-your-docker-host) walks through this for Ollama and LM Studio.
Your browser and the container use the same issuer URL, so give an identity provider a hostname that both reach.

To test an address from the container's point of view, run a throwaway `curl` in its network:

```bash
docker run --rm --network container:umpteenth curlimages/curl -sS https://id.example.com/.well-known/openid-configuration
```

## Starting up

### `app.encryption_key (APP_ENCRYPTION_KEY) is required`

Umpteenth has no encryption key, either because `config.yml` lacks `app.encryption_key` or because Umpteenth found no config file.
If `config.yml` didn't exist before the first `docker compose up`, Docker created a directory named `config.yml` in its place, and Umpteenth doesn't read a directory as a config file.
Remove that directory, copy `config.example.yml` to `config.yml`, fill it in and start again ([Configuration](../configuration/#the-config-file)).

`app.encryption_key (APP_ENCRYPTION_KEY) must be at least 16 bytes` means the key is too short.
`openssl rand -base64 32` prints one that fits.

### `config.yml: line 12: unknown option ...`

The file sets an option that doesn't exist, because of a typo or a line indented under the wrong section.
Compare it with `config.example.yml`.
Other errors at start name the option or its environment variable, such as `unknown log.level (LOG_LEVEL) "verbose", use debug, info, warn or error`.

### A setting has no effect

An environment variable has to spell the option's path in upper case, with underscores for dots, such as `SERVER_TRUST_PROXY` for `server.trust_proxy`.
Umpteenth ignores variables with any other name, without a warning, and treats an empty variable as unset.
A line in the `.env` file next to `docker-compose.yml` reaches Umpteenth only if the service passes it on, through `environment:` or `env_file:` ([Configuration](../configuration/#environment-variables)).

Umpteenth reads `config.yml` at start, so restart the container after an edit.

### `failed to reach the container engine`

Umpteenth can't connect to Docker or Podman, and refuses to start without an engine.
Check that the compose file mounts the engine's socket at `/var/run/docker.sock`, or that `DOCKER_HOST` points at it.
On Podman, start the user's API socket first ([Installation](../../getting-started/installation/#podman)).

### `Failed to prepare the sandbox backend`

The log line comes with `failed to prepare the default sandbox image`, and every run fails with `Failed to create the sandbox: ...`.
The engine lacks the sandbox image, and the pull failed because the project doesn't publish it yet.
Build it as in [Installation](../../getting-started/installation/), with `podman build` if Umpteenth uses Podman.

### `failed to decrypt secret ...`

This error, or `failed to decrypt provider API key`, means the encryption key changed after Umpteenth stored the value.
Put the old `app.encryption_key` back.
If you lost it, create the secrets, model API keys and MCP logins again, since Umpteenth can't decrypt them without the old key ([Backups and migration](../backups/)).

## Signing in

The login page shows the reason a sign-in failed.

### Sign-in is not configured on this server

`oidc.issuer` or `oidc.client_id` is empty.
Fill in the `oidc` block of `config.yml` and restart.

### The service is temporarily unavailable, please try again

Umpteenth couldn't load the identity provider's configuration, and it gives up after 10 seconds.
The issuer URL has to work from inside the container (see [`localhost` inside the container](#localhost-inside-the-container)), and it has to match the `issuer` value the provider publishes character for character, trailing slash included.

### Sign-in failed, please try again

The identity provider sent you back, and Umpteenth couldn't complete the sign-in.
Check three things:

- The client secret in `oidc.client_secret` matches the one in the identity provider.
- You opened Umpteenth at `app.url` and at no other address, since the sign-in starts and ends there.
- You finished at the identity provider within 10 minutes.

### Your account is not allowed to use Umpteenth

`oidc.allowed_groups` lists groups, and the ID token's `groups` claim contains none of them.
Group names have to match as written, upper and lower case included.
Add the user to a listed group, or make the identity provider put the `groups` claim into the ID token.

### The identity provider denied access

The identity provider refused the sign-in, for example because the user isn't assigned to this client.
Fix the assignment in the identity provider.

### The identity provider rejects the redirect URI

Register `<app.url>/api/auth/callback` with the client, for example `https://umpteenth.example.com/api/auth/callback`, and keep `app.url` equal to the URL you open.

### `Too many requests` while signing in

Umpteenth allows 20 requests per minute from one client address to its sign-in endpoints.
Past that, your browser shows a JSON error with the message `Too many requests` in place of the login page.
Behind a reverse proxy without `server.trust_proxy: true`, every user shares the proxy's address and its limit ([Reverse proxy](../reverse-proxy/)).

## Models

### Compiling failed

The alert on the **New job** page reads "The model could not compile the job" when the model call fails, and the container log has the cause in a `Request failed` line.
Umpteenth compiles with the **Utility** default model, or with the **Agent** default if you set no Utility model.
After a fresh install, the usual cause is the **Anthropic** provider without an API key: paste yours as in [Installation](../../getting-started/installation/#connect-a-model).
"Set a utility model in Settings to compile jobs" means you set neither default.
**Fill in manually** skips compiling.

### No model is configured

The run failed before it started with "No model is configured. Pick a model for the job or set a default agent model in Settings."
Neither the job nor **Settings → General → Default models** names an agent model ([Models and costs](../../guides/models/#default-models)).

### Sync failed

A provider row under **Settings → Providers & models** shows **Sync failed**, with the error in its tooltip.
For Ollama and LM Studio, the preset's `localhost` address is the usual cause (see [`localhost` inside the container](#localhost-inside-the-container)).
`the server's model list is not JSON` means the **Base URL** has to end where the server serves `/models`, such as `/v1`.

### `points to a private or local network address`

Saving a provider, an HTTP MCP server or the notification webhook fails with this message when `network.allow_private_targets` is `false`.
Use a public address or turn the option back on ([Security](../security/)).

### The workspace reached its daily spend limit of $5.00

Runs fail at start once the day's spend reaches **Daily spend limit** under **Settings → General → Spend and retention**.
The day resets at 00:00 UTC ([Models and costs](../../guides/models/#daily-spend-limit)).

## Sandboxes and runs

### Internet sandboxes can reach private networks

The **Sandbox backend** card under **Settings → General** shows this alert with **Egress firewall** set to **Not enforced**.
Umpteenth couldn't install the firewall rules that keep sandboxes off your network, which is always the case on Podman.
[Security](../security/) explains the options, and `sandbox.egress_filter: required` makes Umpteenth refuse internet runs until the rules are in place.

### Sandboxes can't start under the configured runtime

Umpteenth's test sandbox failed to start under `sandbox.docker.runtime`, and runs fail until you fix the runtime and restart Umpteenth ([gVisor](../gvisor/)).

### `Environment build failed: ...`

The job's Dockerfile has no successful build.
Read the build log on the job's **Environment** tab ([Sandboxes](../../guides/sandboxes/#a-failed-build-blocks-runs)).

### `The setup script failed with exit code 1`

The setup script in the job's playbook failed, and its step on the **Timeline** shows the output.
Fix the script on the **Playbook** tab or roll back to an earlier version ([How jobs learn](../../guides/self-improvement/#the-playbook-tab)).

### `the run exceeded its limit of 60 turns`

The run hit one of its job's limits.
The turn limit and the cost limit (`the run exceeded its cost limit of $2.00`) end a run as **Failed**, and the time limit (`the run exceeded its time limit`) ends it as **Timed out**.
Raise the limit in the **Sandbox** card on the job's **Settings** tab ([Managing jobs](../../guides/jobs/#limits-per-run)), or make the instruction more specific ([Writing instructions](../../guides/writing-instructions/)).

### `interrupted: the replica executing this run stopped responding`

Umpteenth stopped, crashed or lost a replica while the run was in flight, and it doesn't restart such runs.
The shorter `interrupted: the replica executing this run stopped` has the same cause.
Click **Retry** on the run page if running the job again is safe ([Upgrades and maintenance](../upgrading/#after-a-restart)).

### The retry was skipped because the job is already running

The job's **When runs overlap** setting is **Skip**, and another run of it is active ([Triggers and schedules](../../guides/triggers/#overlapping-runs)).

### The run page stops updating

Behind a reverse proxy, turn off response buffering and allow long read timeouts so live updates get through ([Reverse proxy](../reverse-proxy/)).
Reload the page to see the recorded timeline.

### No events

An old run's **Timeline** is empty once retention has removed its events ([Reading a run](../../guides/runs/#retention)).

### A job never graduates

Work through the checklist on [How jobs learn](../../guides/self-improvement/#if-a-job-doesnt-graduate).

## Webhooks and notifications

### A webhook answers `401`

The response carries `Token is invalid or expired`.
The request needs the job's webhook token in an `Authorization: Bearer <token>` header, and an API token doesn't work there.
A new job has no webhook token until you click **Generate token**, and **Rotate token** invalidates the old one ([Triggers and schedules](../../guides/triggers/#webhooks)).
The webhook URL of a deleted job answers `401` too.

### A webhook answers `429`

Umpteenth accepts 60 webhook calls per minute for each job, with bursts of up to 30.
Wait for the number of seconds in the `Retry-After` header.

### A webhook answers `200` with `"status": "skipped"`

You paused the job, or its **When runs overlap** setting is **Skip** and a run is active.
For a paused job, the response has an empty `runId` and Umpteenth records no run ([Managing jobs](../../guides/jobs/#pausing-a-job)).

### No notifications arrive

Click **Send test** on the **Notifications** card under **Settings → General**.
A failed test shows "The webhook did not accept the notification:" followed by the receiver's error, and the log shows `The notification webhook rejected a delivery` for a message the receiver refused ([Notifications](../../guides/notifications/#delivery-and-retries)).

## MCP servers

### Mentions github, but no MCP server with that name is configured

The compile step matches services in the instruction to MCP servers by name.
Add the server under **MCP Servers** with the name the instruction uses, then compile again ([MCP servers](../../guides/mcp/)).

### `MCP server github is unavailable: ...`

The run's **Timeline** shows this line if a server failed to connect or took longer than two minutes, and the run continued without it.
For an expired login, the message reads "The OAuth login expired. Log in again under MCP Servers."
A stdio server started with `npx` or `uvx` needs **Internet access** in the job to download its package ([MCP servers](../../guides/mcp/#add-a-stdio-server)).

### An MCP login doesn't return to Umpteenth

The provider sends your browser back to `<app.url>/api/mcp-servers/<server id>/oauth/callback`, so `app.url` has to be the address you use for Umpteenth ([MCP servers](../../guides/mcp/#oauth-logins)).
