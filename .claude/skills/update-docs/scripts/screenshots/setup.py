"""Resets the stack and creates the workspace a team would have after two weeks: models, secrets, MCP servers and jobs."""
import json
import secrets as rnd
import string
import urllib.request

from lib import BASE, Api, load_state, save_state, session

# Reset wipes everything and seeds the scripted model as the default
urllib.request.urlopen(urllib.request.Request(BASE + "/api/test/reset", data=b"", method="POST"))
state = {"runs": [], "ids": {}}
state["cookie"] = session()
save_state(state)
a = Api(state["cookie"])

# Models: the scripted provider becomes "Anthropic" with the catalog's labels and prices
prov = a.get("/api/providers")["items"][0]
a.patch(f"/api/providers/{prov['id']}", {"name": "Anthropic"})
caps_big = {"tools": True, "parallelTools": True, "reasoning": True, "jsonSchema": True, "promptCache": True, "vision": True, "context": 1_000_000}
caps_small = dict(caps_big, context=200_000)
models = {}
for key, model, label, price, caps in [
    ("sonnet", "claude-sonnet-5", "Claude Sonnet 5", {"in": 2_000_000, "out": 10_000_000, "cacheRead": 200_000, "cacheWrite": 2_500_000}, caps_big),
    ("haiku", "claude-haiku-4-5", "Claude Haiku 4.5", {"in": 1_000_000, "out": 5_000_000, "cacheRead": 100_000, "cacheWrite": 1_250_000}, caps_small),
    ("opus", "claude-opus-5-5", "Claude Opus 5.5", {"in": 4_000_000, "out": 20_000_000, "cacheRead": 200_000, "cacheWrite": 5_000_000}, caps_big),
]:
    res = a.post("/api/models", {"providerId": prov["id"], "model": model, "label": label, "price": price, "caps": caps})
    models[key] = res["id"]
a.patch("/api/settings", {"agentModelId": models["sonnet"], "utilityModelId": models["haiku"]})
old = [m for m in a.get("/api/models")["items"] if m["model"] == "fake-model"]
for m in old:
    a.delete(f"/api/models/{m['id']}")
state["ids"]["provider"] = prov["id"]
state["ids"]["models"] = models


# Secrets hold throwaway values, since nothing in this workspace talks to the real services
def token(prefix, n):
    return prefix + "".join(rnd.choice(string.ascii_letters + string.digits) for _ in range(n))


secret_ids = {}
for name, value in [("GITHUB_TOKEN", token("ghp_", 36)), ("SLACK_BOT_TOKEN", token("xoxb-", 40)), ("OPENAI_API_KEY", token("sk-proj-", 40))]:
    secret_ids[name] = a.post("/api/secrets", {"name": name, "value": value})["id"]
state["ids"]["secrets"] = secret_ids

# MCP servers: an HTTP one with a bearer header, an OAuth one and two stdio ones
servers = {}
servers["github"] = a.post("/api/mcp-servers", {
    "name": "github", "description": "Pull requests, issues and code in the acme org", "transport": "http",
    "url": "https://api.githubcopilot.com/mcp/", "headers": {"Authorization": "Bearer {{secret:GITHUB_TOKEN}}"}, "enabled": True,
})["id"]
servers["linear"] = a.post("/api/mcp-servers", {
    "name": "linear", "description": "Issues and projects of the platform team", "transport": "http",
    "url": "https://mcp.linear.app/mcp", "enabled": True,
})["id"]
servers["slack"] = a.post("/api/mcp-servers", {
    "name": "slack", "description": "Posts to #eng, #releases and #ops", "transport": "stdio",
    "command": "npx", "args": ["-y", "@modelcontextprotocol/server-slack"],
    "env": {"SLACK_BOT_TOKEN": "{{secret:SLACK_BOT_TOKEN}}", "SLACK_TEAM_ID": "T04ACME7Q2"}, "enabled": True,
})["id"]
servers["fetch"] = a.post("/api/mcp-servers", {
    "name": "fetch", "description": "Reads web pages as Markdown", "transport": "stdio",
    "command": "uvx", "args": ["mcp-server-fetch"], "enabled": True,
})["id"]
state["ids"]["mcp"] = servers
save_state(state)

# Jobs, with the specs the compile step would have produced for them
jobs = {}
jobs["hn"] = a.post("/api/jobs", {
    "name": "Hacker News digest",
    "instruction": "Every weekday at 7:30 Berlin time, read the Hacker News front page and write a digest of its 30 stories as Markdown: the title with its link, points and comment count, sorted by points.\n\nMark stories about self-hosting, Go or databases with a ★ so they stand out. Save the digest as `digest.md` in the run's outputs and report how many stories matched.",
    "cron": "30 7 * * 1-5", "timezone": "Europe/Berlin", "network": "internet", "selfImprove": True,
    "spec": {
        "title": "Hacker News digest",
        "goal": "A Markdown digest of the Hacker News front page every weekday morning, with the stories on our topics marked.",
        "schedule": {"cron": "30 7 * * 1-5", "timezone": "Europe/Berlin", "human": "Weekdays at 07:30"},
        "successCriteria": [
            "digest.md lists the 30 front-page stories with title, link, points and comment count",
            "Stories about self-hosting, Go or databases carry a ★",
            "The outputs report how many stories the digest has and how many matched",
        ],
        "inputs": [],
        "outputs": [
            {"name": "stories", "type": "integer", "description": "Stories in the digest"},
            {"name": "matches", "type": "integer", "description": "Stories about self-hosting, Go or databases"},
        ],
        "mcp": [], "network": "internet", "dockerfile": None, "sideEffects": [],
    },
})["id"]

jobs["pr"] = a.post("/api/jobs", {
    "name": "Stale PR digest",
    "instruction": "Every weekday at 9:00 Berlin time, look at open pull requests in acme/api and acme/web that have had no activity for 3 or more days. Post a short summary to the #eng Slack channel, grouped by author, with a link to each pull request. If there are none, don't post.",
    "cron": "0 9 * * 1-5", "timezone": "Europe/Berlin", "network": "internet", "selfImprove": True,
    "spec": {
        "title": "Stale PR digest",
        "goal": "A weekday Slack summary of the pull requests nobody has touched for three days.",
        "schedule": {"cron": "0 9 * * 1-5", "timezone": "Europe/Berlin", "human": "Weekdays at 09:00"},
        "successCriteria": [
            "Every open pull request in acme/api and acme/web without activity for 3 or more days is listed",
            "The message groups pull requests by author and links each one",
            "Nothing is posted when there are no stale pull requests",
        ],
        "inputs": [],
        "outputs": [
            {"name": "stale", "type": "integer", "description": "Stale pull requests found"},
            {"name": "posted", "type": "boolean", "description": "Whether a message went to #eng"},
        ],
        "mcp": [
            {"server": "github", "why": "List open pull requests and their last activity"},
            {"server": "slack", "why": "Post the summary to #eng"},
        ],
        "network": "internet", "dockerfile": None, "sideEffects": ["Posts one message to #eng in Slack"],
    },
})["id"]

jobs["deps"] = a.post("/api/jobs", {
    "name": "Weekly dependency report",
    "instruction": "Every Monday at 9:30 Berlin time, check the Go modules of acme/api and the npm packages of acme/web for updates. Write a report grouped into major, minor and patch updates with a link to each changelog, and put anything with a published security advisory at the top. Save it as `dependency-report.md`.",
    "cron": "30 9 * * 1", "timezone": "Europe/Berlin", "network": "internet", "selfImprove": True,
    "spec": {
        "title": "Weekly dependency report",
        "goal": "A Monday report of outdated dependencies in acme/api and acme/web, security advisories first.",
        "schedule": {"cron": "30 9 * * 1", "timezone": "Europe/Berlin", "human": "Mondays at 09:30"},
        "successCriteria": [
            "The report covers the Go modules of acme/api and the npm packages of acme/web",
            "Updates are grouped into major, minor and patch, each with a changelog link",
            "Dependencies with a security advisory are listed first",
        ],
        "inputs": [],
        "outputs": [
            {"name": "outdated", "type": "integer", "description": "Dependencies with an update"},
            {"name": "advisories", "type": "integer", "description": "Dependencies with a security advisory"},
        ],
        "mcp": [{"server": "github", "why": "Read go.mod and package.json and look up security advisories"}],
        "network": "internet", "dockerfile": None, "sideEffects": [],
    },
})["id"]

jobs["uptime"] = a.post("/api/jobs", {
    "name": "Uptime check",
    "instruction": "Every two hours between 8:00 and 18:00 on weekdays, Berlin time, check that https://example.com and https://example.org answer with HTTP 200 within 2 seconds. Keep the last status of each site in state and only report a site when its status changes.",
    "cron": "0 8-18/2 * * 1-5", "timezone": "Europe/Berlin", "network": "internet", "selfImprove": True,
    "modelId": models["haiku"],
    "spec": {
        "title": "Uptime check",
        "goal": "Notice within two hours when example.com or example.org stops answering.",
        "schedule": {"cron": "0 8-18/2 * * 1-5", "timezone": "Europe/Berlin", "human": "Every 2 hours from 08:00 to 18:00 on weekdays"},
        "successCriteria": [
            "Both sites are checked for HTTP 200 within 2 seconds",
            "The last status of each site is kept in state",
            "A site is only reported when its status changed since the last run",
        ],
        "inputs": [],
        "outputs": [
            {"name": "up", "type": "integer", "description": "Sites that answered 200 in time"},
            {"name": "changed", "type": "boolean", "description": "Whether a site's status changed"},
        ],
        "mcp": [], "network": "internet", "dockerfile": None, "sideEffects": [],
    },
})["id"]

jobs["release"] = a.post("/api/jobs", {
    "name": "Release notes",
    "instruction": "When triggered by a webhook with a GitHub release payload, summarize the changes since the previous release in plain language for our customers. Group them into New, Improved and Fixed, leave out internal refactors, and return the summary as the output \"notes\".",
    "network": "internet", "selfImprove": True, "concurrency": "queue",
    "spec": {
        "title": "Release notes",
        "goal": "Customer-facing release notes for every GitHub release of acme/api.",
        "successCriteria": [
            "The notes cover every merged pull request since the previous release",
            "Changes are grouped into New, Improved and Fixed",
            "Internal refactors are left out",
        ],
        "inputs": [{"name": "release", "type": "object", "description": "The GitHub release event payload"}],
        "outputs": [{"name": "notes", "type": "string", "description": "The release notes as Markdown"}],
        "mcp": [{"server": "github", "why": "Compare the release with the previous tag and read the merged pull requests"}],
        "network": "internet", "dockerfile": None, "sideEffects": [],
    },
})["id"]
state["ids"]["jobs"] = jobs

# The release job is started by CI through its webhook
state["ids"]["releaseToken"] = a.post(f"/api/jobs/{jobs['release']}/webhook-token")["token"]
save_state(state)
print(json.dumps(state["ids"], indent=2))
