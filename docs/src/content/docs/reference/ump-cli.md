---
title: ump CLI
seoTitle: ump CLI for sandbox scripts
description: The command-line tool in every sandbox that lets scripts call MCP tools and models, keep job state and report outputs.
---

Scripts in a job's sandbox reach Umpteenth through `ump`, which calls the job's MCP tools and models and stores job state and the run's outputs.
The agent and reflection write those scripts, so you'll open this page most often to make sense of a timeline or to edit a playbook script.

Umpteenth copies `ump` to `/usr/local/bin/ump` when it creates a sandbox, so a custom amd64 or arm64 Linux image doesn't need to include it.
Each command sends a request to Umpteenth on the host with the run's token, so model API keys and the credentials of HTTP MCP servers stay on the host.

## On the timeline

A `ump` call shows up on the run's **Timeline** as a step titled `ump` plus the method and path it called, such as `ump POST /v1/mcp/call` for `ump mcp call` or `ump PUT /v1/state/<key>` for `ump state set`.
The step shows how long the call took and, depending on the command, the MCP server and tool or the model and its usage.
A failed call turns red and shows the error, and an MCP call adds a **Result of** block with what the tool returned.
`ump step` adds a **Step** marker with the name you pass, next to its own step.

A run gets a step for each of its first 1,000 calls.
Past that, the timeline keeps only MCP calls that may change something, paid `ump llm` calls and the first `ump state set`, while the commands keep working and `ump step` keeps adding its markers.

## Arguments and exit codes

`ump help` lists the commands.
Each command exits with `0` on success, `1` when the call fails, and `2` on a usage error such as a missing argument.
A missing state key and `ump fail` count as failures.

An argument written as `[value|-]` comes from stdin when you pass `-` or leave it out, and `ump` drops the trailing newlines.
`ump mcp call` reads stdin only with an explicit `-`, and sends `{}` when you give no arguments at all.

Outside a sandbox, the commands fail with "UMP_BROKER_URL and UMP_TOKEN are not set; ump only works inside an Umpteenth sandbox".

## ump mcp

```txt
ump mcp tools [server]
ump mcp call <server> <tool> [json|-]
```

`tools` prints a JSON array of the tools the job may use, each with `server`, `name`, `description`, `inputSchema` and `readOnly`.
Pass a server name to list that server's tools alone.

`call` takes the server's name from **MCP servers**, the tool's own name and the arguments as JSON:

```bash
ump mcp call github list_pull_requests '{"owner": "acme", "repo": "api", "state": "open"}'
```

`ump` prints the tool's result as text, which is JSON only when the tool returns JSON, so look at a tool's output once before you pipe it into `jq`.
If the tool reports an error, `ump` prints the message and exits with `1`.
An unknown pair of server and tool fails with `MCP tool <tool> on server <server> not found`.

Umpteenth records each call to a tool that its MCP server doesn't mark as read-only.
If a scripted run falls back to the agent, the agent gets that list so it doesn't repeat those calls.
A shadow run, which tries out a new main script before Umpteenth applies it, can't call such tools at all ([How jobs learn](../../guides/self-improvement/)).

## ump llm

```txt
ump llm [--model utility|agent] [--schema <file>] [--system <text>] [--max-tokens <n>] [prompt|-]
```

`ump llm` makes one model call without tools, at low effort, and prints the answer.

| Flag | Effect |
|---|---|
| `--model utility` | The default. Uses the **Utility** model under **Settings → General → Default models**, or the run's own model if the workspace has none. |
| `--model agent` | Uses the run's own model, the one the agent works with |
| `--schema <file>` | A JSON Schema file with an object at the top. `ump` prints the answer as indented JSON that matches it. |
| `--system <text>` | A system prompt |
| `--max-tokens <n>` | The longest answer in tokens, 4096 by default and 16,000 at most |

The prompt is the remaining arguments joined by spaces, or stdin when there are none or you pass `-`.
Any other `--model` value, a `--max-tokens` that isn't a positive number, or a schema file that isn't valid JSON exits with `2`.

A classification step in a main script looks like this:

```bash
cat > /workspace/triage.schema.json <<'EOF'
{"type": "object", "properties": {"category": {"type": "string"}, "urgent": {"type": "boolean"}}, "required": ["category", "urgent"]}
EOF
jq -r .body /ump/input.json | ump llm --schema /workspace/triage.schema.json --system "Classify this support request" -
```

```json
{
  "category": "billing",
  "urgent": false
}
```

Each call times out after 5 minutes, and its cost counts toward the run and the job's **Max cost per run** ([Models and costs](../../guides/models/)).
Once the run reaches that limit, `ump llm` fails with "the run reached its cost limit, so ump llm is no longer available".
A call that breaks off midway, such as on a timeout, costs an estimate of its maximum from the prompt size and `--max-tokens`, since the provider may bill it.

## ump state

```txt
ump state get <key>
ump state set <key> [value|-]
ump state list
```

Job state holds string values that a job keeps from one run to the next, such as the ID of the last item it handled.
The agent reads and writes the same values, and you can view and edit them on the job's **State** tab.

`get` prints the value.
For a missing key it prints nothing and exits with `1`, so a script can test for it:

```bash
if last=$(ump state get last_seen_id); then
  echo "Continuing after $last"
else
  echo "First run"
fi
```

`list` prints every key and value as a JSON object.
Keys take up to 200 characters and values up to 1 MiB, and a job keeps at most 1,000 keys with 16 MiB of keys and values in total.

## ump output

```txt
ump output set <key> [value|-]
```

`ump output set` sets one of the run's outputs, which appear on the run's **Outputs** tab and in the `outputs` field of the [REST API](../api/#read-a-run).
A value that parses as JSON stays JSON, so `3` becomes a number, `true` a boolean and `{"new": 4}` an object.
Anything else becomes a string.

Setting a key again replaces its value, and a run keeps up to 100 outputs with 1 MiB in total.

## ump step

```txt
ump step <name>
```

`ump step` joins its arguments with spaces and adds a **Step** marker with that name to the timeline.
If a scripted run falls back to the agent, Umpteenth tells the agent the last step the script reached.
Names take up to 200 bytes, and a run records at most 1,000 steps.

## ump summary

```txt
ump summary [text|-]
```

`ump summary` sets the Markdown summary of a scripted run, up to 16 KiB.
A scripted run without one takes the verifier's summary, or else "The main script did the job." followed by the end of the script's output.
In agent runs the command has no effect, and the run keeps the summary the agent gives when it finishes.

## ump fail

```txt
ump fail <reason>
```

`ump fail` records a failure with your reason, or `the script reported a failure` if you give none, and exits with `1`, so a `set -e` script stops there.

In a scripted run, the failure fails verification and the agent takes over in the same sandbox, so the run can still end as `succeeded`.
In an agent run, a run that would have succeeded ends as `failed` with the reason as its error.

## ump connect

```txt
ump connect <host> <port>
```

`ump connect` opens a TCP connection to the host through the egress proxy and relays it on stdin and stdout, for tools that take a command to connect with.
In sandboxes with **Internet access** or **Allowed domains only**, Umpteenth sets it as SSH's `ProxyCommand` for every host, so `ssh` and `git` over SSH work without further setup.

The proxy checks the host like any other connection of the job.
A refusal ends the command with exit code `1` and an error such as `the egress proxy refused github.com:22: 403 Forbidden: github.com is not on this job's allow-list`.

## Paths

| Path | Contents |
|---|---|
| `/workspace` | The working directory of the agent's commands and the job's scripts |
| `/ump/input.json` | The run's input: a webhook body, the input of **Run now** or the API, or `{}` |
| `/ump/outputs/` | Files Umpteenth collects as the run's artifacts when the run ends, up to 50 MiB in total |
| `/ump/PLAYBOOK.md` | The job's current playbook as Markdown |
| `/ump/toolkit/<name>` | The playbook's toolkit scripts, which take their arguments as `--name value` flags |
| `/ump/main` | The main script of a graduated job |
| `/ump/logs/` | The full output of each command the agent runs and of the main script |

Commands run as the user `agent` (uid 1000) unless the job has **Run as root** on.

## Environment

| Variable | Contents |
|---|---|
| `UMP_BROKER_URL` | The address `ump` calls on the host |
| `UMP_TOKEN` | The run's token, which works while the run executes |
| `UMP_RUN_ID` | The run's ID |
| `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY` and their lowercase forms | Only in jobs with **Internet access** or **Allowed domains only**: the egress proxy at `http://ump:<token>@umpteenth:8081` with the run's token and the broker port, `socks5h://` in place of `http://` for `ALL_PROXY`, and `umpteenth,localhost,127.0.0.1` for `NO_PROXY` |
| `NODE_USE_ENV_PROXY` | `1` in the same jobs, so Node 24 and later use the proxy variables |

The job's secrets arrive as environment variables too, under the names the job maps them to, and a secret mapped to one of the names above never replaces it.
[Sandboxes](../../guides/sandboxes/#secrets) covers secrets, and [Security](../../deployment/security/#the-egress-proxy) lists the addresses the proxy refuses.
