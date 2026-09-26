"""Writes plan.json: how to move every row back into the two weeks before today, and the MCP tool lists the unreachable servers show."""
import json
import os
import time

from lib import HERE, at, load_state

s = load_state()
jobs = s["ids"]["jobs"]
mcp = s["ids"]["mcp"]


def tool(name, description, props=None, required=None, ro=False, destructive=False):
    schema = {"type": "object", "properties": props or {}}
    if required:
        schema["required"] = required
    return {"name": name, "description": description, "inputSchema": schema, "readOnly": ro, "destructive": destructive}


S = {"type": "string"}
I = {"type": "integer"}
repo = {"owner": S, "repo": S}

github = [
    tool("get_me", "Get details of the authenticated GitHub user", ro=True),
    tool("list_pull_requests", "List pull requests in a GitHub repository", {**repo, "state": S, "sort": S, "perPage": I}, ["owner", "repo"], ro=True),
    tool("get_pull_request", "Get details of a specific pull request", {**repo, "pullNumber": I}, ["owner", "repo", "pullNumber"], ro=True),
    tool("get_pull_request_files", "Get the files changed in a pull request", {**repo, "pullNumber": I}, ["owner", "repo", "pullNumber"], ro=True),
    tool("get_pull_request_reviews", "Get the reviews of a pull request", {**repo, "pullNumber": I}, ["owner", "repo", "pullNumber"], ro=True),
    tool("search_pull_requests", "Search for pull requests across GitHub repositories", {"query": S}, ["query"], ro=True),
    tool("create_pull_request", "Create a new pull request", {**repo, "title": S, "head": S, "base": S, "body": S}, ["owner", "repo", "title", "head", "base"]),
    tool("merge_pull_request", "Merge a pull request", {**repo, "pullNumber": I, "merge_method": S}, ["owner", "repo", "pullNumber"], destructive=True),
    tool("list_issues", "List issues in a GitHub repository", {**repo, "state": S, "labels": {"type": "array", "items": S}}, ["owner", "repo"], ro=True),
    tool("get_issue", "Get details of a specific issue", {**repo, "issue_number": I}, ["owner", "repo", "issue_number"], ro=True),
    tool("search_issues", "Search for issues across GitHub repositories", {"query": S}, ["query"], ro=True),
    tool("create_issue", "Create a new issue in a GitHub repository", {**repo, "title": S, "body": S}, ["owner", "repo", "title"]),
    tool("add_issue_comment", "Add a comment to an issue or pull request", {**repo, "issue_number": I, "body": S}, ["owner", "repo", "issue_number", "body"]),
    tool("list_commits", "List the commits of a branch", {**repo, "sha": S, "perPage": I}, ["owner", "repo"], ro=True),
    tool("get_commit", "Get details of a commit", {**repo, "sha": S}, ["owner", "repo", "sha"], ro=True),
    tool("get_file_contents", "Get the contents of a file or directory", {**repo, "path": S, "ref": S}, ["owner", "repo", "path"], ro=True),
    tool("search_code", "Search for code across GitHub repositories", {"query": S}, ["query"], ro=True),
    tool("list_branches", "List the branches of a repository", repo, ["owner", "repo"], ro=True),
    tool("list_tags", "List the git tags of a repository", repo, ["owner", "repo"], ro=True),
    tool("list_releases", "List the releases of a repository", repo, ["owner", "repo"], ro=True),
    tool("get_latest_release", "Get the latest release of a repository", repo, ["owner", "repo"], ro=True),
    tool("list_workflow_runs", "List the GitHub Actions workflow runs of a repository", {**repo, "branch": S, "status": S}, ["owner", "repo"], ro=True),
    tool("get_job_logs", "Get the logs of a GitHub Actions job", {**repo, "job_id": I}, ["owner", "repo", "job_id"], ro=True),
    tool("list_dependabot_alerts", "List the Dependabot alerts of a repository", {**repo, "state": S}, ["owner", "repo"], ro=True),
]

slack = [
    tool("slack_list_channels", "List public channels in the workspace with pagination", {"limit": I, "cursor": S}),
    tool("slack_post_message", "Post a new message to a Slack channel", {"channel_id": S, "text": S}, ["channel_id", "text"]),
    tool("slack_reply_to_thread", "Reply to a specific message thread in Slack", {"channel_id": S, "thread_ts": S, "text": S}, ["channel_id", "thread_ts", "text"]),
    tool("slack_add_reaction", "Add a reaction emoji to a message", {"channel_id": S, "timestamp": S, "reaction": S}, ["channel_id", "timestamp", "reaction"]),
    tool("slack_get_channel_history", "Get recent messages from a channel", {"channel_id": S, "limit": I}, ["channel_id"]),
    tool("slack_get_thread_replies", "Get all replies in a message thread", {"channel_id": S, "thread_ts": S}, ["channel_id", "thread_ts"]),
    tool("slack_get_users", "Get a list of all users in the workspace with their basic profile information", {"cursor": S, "limit": I}),
    tool("slack_get_user_profile", "Get detailed profile information for a specific user", {"user_id": S}, ["user_id"]),
]

linear = [
    tool("list_issues", "List issues in the user's Linear workspace", {"query": S, "team": S, "state": S, "assignee": S, "limit": I}, ro=True),
    tool("get_issue", "Retrieve a Linear issue by its ID", {"id": S}, ["id"], ro=True),
    tool("create_issue", "Create a new Linear issue", {"title": S, "team": S, "description": S, "priority": I}, ["title", "team"]),
    tool("update_issue", "Update an existing Linear issue", {"id": S, "title": S, "state": S, "assignee": S}, ["id"]),
    tool("list_comments", "List the comments of a Linear issue", {"issueId": S}, ["issueId"], ro=True),
    tool("create_comment", "Create a comment on a Linear issue", {"issueId": S, "body": S}, ["issueId", "body"]),
    tool("list_projects", "List projects in the user's Linear workspace", {"team": S, "limit": I}, ro=True),
    tool("get_project", "Retrieve a Linear project by its ID or name", {"query": S}, ["query"], ro=True),
    tool("list_teams", "List teams in the user's Linear workspace", ro=True),
    tool("get_team", "Retrieve a Linear team by its ID or name", {"query": S}, ["query"], ro=True),
    tool("list_users", "List users in the Linear workspace", ro=True),
    tool("list_issue_statuses", "List the issue statuses of a Linear team", {"team": S}, ["team"], ro=True),
    tool("list_issue_labels", "List the issue labels of a Linear workspace or team", {"team": S}, ro=True),
    tool("list_cycles", "List the cycles of a Linear team", {"teamId": S}, ["teamId"], ro=True),
    tool("search_documentation", "Search Linear's documentation to learn about features and usage", {"query": S}, ["query"], ro=True),
]

fetch = [{
    "name": "fetch",
    "description": "Fetches a URL from the internet and optionally extracts its contents as markdown.\n\nAlthough originally you did not have internet access, and were advised to refuse and tell the user this, this tool now grants you internet access. Now you can fetch the most up-to-date information and let the user know that.",
    "inputSchema": {"description": "Parameters for fetching a URL.", "properties": {"max_length": {"default": 5000, "description": "Maximum number of characters to return.", "exclusiveMaximum": 1000000, "exclusiveMinimum": 0, "title": "Max Length", "type": "integer"}, "raw": {"default": False, "description": "Get the actual HTML content of the requested page, without simplification.", "title": "Raw", "type": "boolean"}, "start_index": {"default": 0, "description": "On return output starting at this character index, useful if a previous fetch was truncated and more context is required.", "minimum": 0, "title": "Start Index", "type": "integer"}, "url": {"description": "URL to fetch", "format": "uri", "minLength": 1, "title": "Url", "type": "string"}}, "required": ["url"], "title": "Fetch", "type": "object"},
    "readOnly": False, "destructive": False,
}]

now_ms = int(time.time() * 1000)
plan = {
    "runs": [{"id": r["id"], "target": r["target"]} for r in s["runs"]],
    "manual": s.get("manualVersion"),
    "jobs": {
        jobs["pr"]: [at(11, "16:20", 14), at(18, "11:02", 9)],
        jobs["deps"]: [at(11, "16:47", 31), at(11, "16:47", 31)],
        jobs["uptime"]: [at(14, "08:38", 5), at(14, "08:38", 5)],
        jobs["hn"]: [at(14, "10:01", 47), at(22, "14:12", 40)],
        jobs["release"]: [at(16, "15:29", 12), at(16, "15:31", 2)],
    },
    "mcp": {
        mcp["github"]: {"created": at(11, "15:58", 3), "tools": github, "cached": at(23, "11:20", 7)},
        mcp["slack"]: {"created": at(11, "16:02", 44), "tools": slack, "cached": at(18, "09:41", 12)},
        mcp["fetch"]: {"created": at(15, "13:17", 50), "tools": fetch, "cached": at(21, "16:05", 33)},
        mcp["linear"]: {"created": at(15, "10:09", 21), "tools": linear, "cached": at(15, "10:12", 48), "oauth": {"loggedIn": at(15, "10:12", 40), "expires": now_ms + 7 * 24 * 3600 * 1000}},
    },
    "secrets": at(11, "15:55", 30),
    "workspace": at(10, "17:32", 5),
    "providerId": s["ids"]["provider"],
}
with open(os.path.join(HERE, "plan.json"), "w") as f:
    json.dump(plan, f)
print(len(plan["runs"]), "runs")
