---
title: Connect an agent
seoTitle: Connect Claude Code, Cursor or Codex over MCP
description: Let Claude Code, Claude Desktop, Cursor, Codex and other MCP clients create, run and follow your Umpteenth jobs through the MCP server at /api/mcp.
---

Umpteenth runs an MCP server at `/api/mcp`, so a coding agent such as Claude Code can set up jobs, run them and read their results for you.
The agent connects with an [API token](../api/#api-tokens), or signs you in through your identity provider once you [set that up](#sign-in-through-your-identity-provider).
Either way it acts with your role in the workspace, the same as you calling the [REST API](../api/).

## Connect with an API token

Create a token under **Settings → API tokens**.
The server's URL is your `app.url` followed by `/api/mcp`.

Claude Code adds the server with one command:

```bash
claude mcp add --transport http umpteenth https://umpteenth.example.com/api/mcp --header "Authorization: Bearer $UMPTEENTH_API_TOKEN"
```

Cursor reads it from its MCP config:

```json title="~/.cursor/mcp.json"
{
  "mcpServers": {
    "umpteenth": {
      "url": "https://umpteenth.example.com/api/mcp",
      "headers": { "Authorization": "Bearer ump_…" }
    }
  }
}
```

Codex takes the token from an environment variable, here `UMPTEENTH_TOKEN`:

```toml title="~/.codex/config.toml"
[mcp_servers.umpteenth]
url = "https://umpteenth.example.com/api/mcp"
bearer_token_env_var = "UMPTEENTH_TOKEN"
```

Any other client that speaks Streamable HTTP and sends an `Authorization` header connects the same way.

Then ask for a job in plain words, such as "Create an Umpteenth job that posts last night's failed CI builds to Slack at 8:00 on weekdays, then run it once."
The agent compiles the description, creates the job, attaches the MCP servers, secrets and skills it needs and follows the run until it finishes.

## Sign in through your identity provider

Claude Desktop and claude.ai can't send a token and connect only through an OAuth sign-in.
Umpteenth hands that sign-in to one of your [OpenID Connect providers](../../deployment/sign-in/#openid-connect), which has to issue access tokens for an API and let MCP clients register themselves.
[Pocket ID](https://pocket-id.org) does both, and the steps below use it.

1. In Pocket ID, open **Administration → APIs** and add an API whose **API resource** is your `app.url` followed by `/api/mcp`, such as `https://umpteenth.example.com/api/mcp`.
   Under **Access**, pick **Metadata document clients** and turn on **Allow all metadata document clients**.
   The API needs no permissions.
2. Add the client metadata URLs of the clients you use to Pocket ID's `CIMD_URL_ALLOWLIST`, for Claude `https://claude.ai/oauth/mcp-oauth-client-metadata` and `https://claude.ai/oauth/claude-code-client-metadata`.
3. Name the provider's ID from `auth.providers` in `mcp.oauth_provider` and restart Umpteenth:

   ```yaml title="config.yml"
   mcp:
     oauth_provider: pocket-id
   ```

4. In Claude Desktop, add the URL under **Settings → Connectors** as a custom connector and click **Connect**.
   In Claude Code, leave out the header and sign in with `claude mcp login`:

   ```bash
   claude mcp add --transport http umpteenth https://umpteenth.example.com/api/mcp
   ```

   ```bash
   claude mcp login umpteenth
   ```

The client acts as the account you signed in to Umpteenth with through that provider, so sign in to Umpteenth in the browser once first.
It works in the workspace you last opened in the browser, or in the one whose ID it sends as `X-Umpteenth-Workspace`, with your role there and without instance admin rights.
Passkey and GitHub accounts connect with an API token.

## Tools

Each tool calls the route of the same name on the [API endpoints](../api-endpoints/) page, so it runs the same checks and returns the same JSON.

| Tool | Effect |
|---|---|
| `list_models`, `list_mcp_servers`, `list_secrets`, `list_skills` | Lists what a job can use, with secrets by name only |
| `list_jobs`, `get_job` | Finds jobs and reads one |
| `compile_job` | Turns an instruction into a spec, like the **New job** page, without saving anything |
| `create_job`, `update_job` | Saves a new job or changes an existing one |
| `get_job_mcp_servers`, `set_job_mcp_servers` | Reads or replaces the MCP servers a job gets |
| `get_job_secrets`, `set_job_secrets` | Reads or replaces the secrets a job gets as environment variables |
| `get_job_skills`, `set_job_skills` | Reads or replaces the skills a job gets |
| `run_job` | Starts a run, which the run page shows as **Triggered via the API** |
| `list_runs`, `get_run`, `list_run_events` | Follows runs and reads their results and timelines |
| `cancel_run` | Stops a queued or live run |

A route's path and query parameters and its JSON body fields become the tool's arguments.
The `set_job_*` tools replace the whole list, which they take as `body`.
A call that breaks a rule, such as a field over its length limit, comes back as a tool error with the message and the field names, and the agent can fix the call and try again.
