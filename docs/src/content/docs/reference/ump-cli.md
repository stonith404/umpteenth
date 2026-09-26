---
title: ump CLI
description: The command-line tool in every sandbox that lets scripts call MCP tools and models, keep job state and report outputs.
---

The agent and reflection write the scripts that run in a job's sandbox, so you'll open this page most often to make sense of a timeline.
Those scripts reach Umpteenth through `ump`, which calls the job's MCP tools and models and stores job state and the run's outputs.

Umpteenth copies `ump` to `/usr/local/bin/ump` when it creates a sandbox, so a custom image doesn't need to include it.
It runs in amd64 and arm64 Linux images.
Each command sends a request to Umpteenth on the host with the run's token, so model API keys and the credentials of HTTP MCP servers stay on the host.

## On the timeline

Each `ump` call shows up on the run's **Timeline** as a step titled `ump` plus the method and path it called:

| Timeline step | Command |
|---|---|
| `ump GET /v1/mcp/tools` | `ump mcp tools` |
| `ump POST /v1/mcp/call` | `ump mcp call` |
| `ump POST /v1/llm` | `ump llm` |
| `ump GET /v1/state`, `ump GET /v1/state/<key>`, `ump PUT /v1/state/<key>` | `ump state list`, `get` and `set` |
| `ump POST /v1/output` | `ump output set` |
| `ump POST /v1/step` and a **Step** marker | `ump step` |
| `ump POST /v1/summary` | `ump summary` |
| `ump POST /v1/fail` | `ump fail` |

The step shows how long the call took and, depending on the command, the MCP server and tool or the model with its tokens and cost.
The step of a failed call turns red and shows the error, and an MCP call adds a **Result of** block with what the tool returned.
[Reading a run](../../guides/runs/) covers the rest of the run page.

## Arguments and exit codes

`ump help` lists the commands.
Each command exits with `0` on success, `1` when the call fails, and `2` on a usage error such as a missing argument.
A missing state key and `ump fail` count as failures.

An argument written as `[value|-]` comes from stdin when you pass `-` or leave it out, and `ump` drops the trailing newlines.
`ump mcp call` reads stdin only with an explicit `-`, and sends `{}` when you give no arguments at all.

Outside a sandbox, commands fail with "UMP_BROKER_URL and UMP_TOKEN are not set; ump only works inside an Umpteenth sandbox".

## ump mcp

```txt
ump mcp tools [server]
ump mcp call <server> <tool> [json|-]
```

`tools` prints a JSON array of the tools the job may use, each with `server`, `name`, `description`, `inputSchema` and `readOnly`.
Pass a server name to list that server's tools alone.

`call` takes the server's name from **MCP Servers**, the tool's own name and the arguments as JSON:

```bash
ump mcp call github list_pull_requests '{"owner": "acme", "repo": "api", "state": "open"}'
```

`ump` prints the tool's result as text.
Text content prints as it is, and a result with only structured content prints as JSON.
Images, audio and resources without text print as placeholders such as `[image image/png, 1234 bytes]`.
The output is JSON only when the tool returns JSON text, so look at a tool's output once before you pipe it into `jq`.
If the tool reports an error, `ump` prints the message and exits with `1`.
An unknown pair of server and tool fails with `MCP tool <tool> on server <server> not found`.

Umpteenth records each call to a tool that its MCP server doesn't mark as read-only.
If a scripted run falls back to the agent, the agent gets that list with the instruction not to repeat those calls.
A shadow run is a trial of a new main script that reflection wrote, before Umpteenth applies it.
In a shadow run, Umpteenth refuses calls to those tools with "A shadow run of the main script can't call MCP tools that may have side effects".
See [How jobs learn](../../guides/self-improvement/) for fallback and shadow runs.

## ump llm

```txt
ump llm [--model utility|agent] [--schema <file>] [--system <text>] [--max-tokens <n>] [prompt|-]
```

`ump llm` makes one model call without tools, at low effort, and prints the answer.

| Flag | Effect |
|---|---|
| `--model utility` | The default. Uses the **Utility** model under **Settings → General → Default models**, or the run's own model when none is set. Any value other than `agent` counts as `utility`. |
| `--model agent` | Uses the run's own model, the one the agent would use |
| `--schema <file>` | A JSON Schema file. `ump` prints the answer as indented JSON that matches it, and exits with `2` if the file isn't valid JSON. |
| `--system <text>` | A system prompt |
| `--max-tokens <n>` | The longest answer, 4096 tokens by default. A value above 16,000 or below 1 falls back to 4096. |

The prompt is the remaining arguments joined by spaces, or stdin when there are none or you pass `-`.
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

Each call times out after 5 minutes.
Its cost counts toward the run and the job's **Max cost per run** limit.
While a call runs, Umpteenth sets aside an estimate of the most it could cost, so parallel calls can't spend past the limit together.
Once the run reaches its limit, `ump llm` fails with "the run reached its cost limit, so ump llm is no longer available".

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
Keys take up to 200 characters and values up to 1 MiB, and a job keeps at most 1,000 keys.

## ump output

```txt
ump output set <key> [value|-]
```

`ump output set` sets one of the run's outputs, which appear on the run's **Outputs** tab and in the `outputs` field of the [REST API](../api/#read-a-run).
A value that parses as JSON stays JSON, so `3` becomes a number, `true` a boolean and `{"new": 4}` an object.
Anything else becomes a string.

Setting a key again replaces its value.
A run keeps up to 100 outputs with 1 MiB in total.
In an agent run, an output the agent reports when it finishes replaces a script's output of the same key.

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
In agent runs the command has no effect, and the run keeps the summary the agent writes when it finishes.

## ump fail

```txt
ump fail <reason>
```

`ump fail` records a failure with your reason, or `the script reported a failure` if you give none, and exits with `1`, so a `set -e` script stops there.

In a scripted run, the reported failure fails verification with `the script reported a failure: <reason>`, and the agent takes over in the same sandbox.
From then on the agent's result counts, and the run can end as `succeeded`.
In an agent run, where the agent calls `ump fail` from its shell, a run that would have succeeded ends as `failed` with the reason as its error.

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
| `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, `https_proxy` | Jobs with **Allowed domains only**: Umpteenth's egress proxy for outbound traffic, which lets through only the job's allowed domains |
| `NO_PROXY`, `no_proxy` | `umpteenth,localhost,127.0.0.1` in the same jobs |
| `NODE_USE_ENV_PROXY` | `1` in the same jobs, so Node honors the proxy variables |

The proxy refuses a host that isn't on the list with `403` and `<host> is not on this job's allow-list`, and it never lets through a bare IP address.

The job's secrets are plain environment variables under the names the job maps them to, so any command in the sandbox can read and print them.
Umpteenth drops a mapping that would replace one of the `UMP_` variables above, or a proxy variable in an **Allowed domains only** job.
[Sandboxes](../../guides/sandboxes/#secrets) covers secrets and network modes.
