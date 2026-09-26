"""Records the landing page's live demo: the digest's first run and its first scripted run, written to frontend/src/demo/fixtures.ts."""
import hashlib
import json
import os
import re
import string
import subprocess
import sys

from lib import HERE, api

# Runs after hn.py, which leaves the digest with its exploring run #1 and its first scripted run #6
OUT = sys.argv[1] if len(sys.argv) > 1 else os.path.normpath(os.path.join(HERE, "../../../../../frontend/src/demo/fixtures.ts"))
FRONTEND = os.path.normpath(os.path.join(os.path.dirname(OUT), "../.."))
EXPLORE_RUN, SCRIPTED_RUN = 1, 6

# The same fictional stories shots.mjs puts on the screenshots, in the order the real ones first appear
STORIES = [
    ("Show HN: A 400-line RSS reader for a Raspberry Pi", "https://blog.example.dev/rss-pi"),
    ("The quiet joy of cron", "https://example.net/cron"),
    ("Reading your own flame graphs", "https://example.com/flame-graphs"),
    ("A field guide to retry budgets", "https://example.org/retry-budgets"),
    ("Notes from running Postgres on a laptop for a year", "https://notes.example.org/laptop-postgres"),
    ("An illustrated guide to TCP keepalives", "https://example.dev/keepalives"),
]
TITLE = re.compile(r'"title":\s?"((?:[^"\\]|\\.)*)",\s*"url":\s?"((?:[^"\\]|\\.)*)"')
LINK = re.compile(r"\[([^\]]+)\]\((https?://[^)]+)\)")
POINTS = re.compile(r'("(?:points|score)":\s?)(\d+)|(\d+)( points)')
COMMENTS = re.compile(r'("(?:num_comments|comments|descendants)":\s?)(\d+)|(\d+)( comments)')
# Fictional counts, handed out from the top so the digest stays sorted by points
FAKE_POINTS = [331, 320, 223, 187, 164, 142, 121, 96, 88, 73]
FAKE_COMMENTS = [22, 282, 128, 64, 51, 37, 19, 12, 9, 4]
ITEM_ID = re.compile(r"\b\d{8}\b")
CALL_ID = re.compile(r"fake_call_\d+")
HEX_ID = re.compile(r"\b[0-9a-f]{64}\b")
PEOPLE = [("E2E User", "Ada Park"), ("e2e@example.com", "ada@example.com")]


def strings(value):
    """Every string in a JSON value, keys excluded."""
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for v in value.values():
            yield from strings(v)
    elif isinstance(value, list):
        for v in value:
            yield from strings(v)


def rewrite(value, fn):
    if isinstance(value, str):
        return fn(value)
    if isinstance(value, dict):
        return {k: rewrite(v, fn) for k, v in value.items()}
    if isinstance(value, list):
        return [rewrite(v, fn) for v in value]
    return value


def stable(text, modulo):
    return int(hashlib.sha256(text.encode()).hexdigest(), 16) % modulo


def cleaner(recorded):
    """Replaces what the live front page put into the runs: headlines, links, counts and item IDs."""
    # The stories the sandbox fetched, as JSON fields in command output and as links in the digest
    swaps = {}
    for text in strings(recorded):
        for title, url in TITLE.findall(text) + LINK.findall(text):
            if title not in swaps and len(swaps) // 2 < len(STORIES):
                fake_title, fake_url = STORIES[len(swaps) // 2]
                swaps[title], swaps[url] = fake_title, fake_url

    # Real counts map to fictional ones by rank, so the highest real score gets the highest fictional one
    def ranks(pattern, fakes):
        found = {int(m.group(2) or m.group(3)) for text in strings(recorded) if text.startswith("exit code") for m in pattern.finditer(text)}
        return {str(real): str(fakes[min(i, len(fakes) - 1)]) for i, real in enumerate(sorted(found, reverse=True))}

    points, comments = ranks(POINTS, FAKE_POINTS), ranks(COMMENTS, FAKE_COMMENTS)

    def count(table):
        def sub(m):
            if m.group(1):
                return m.group(1) + table.get(m.group(2), m.group(2))
            return table.get(m.group(3), m.group(3)) + m.group(4)
        return sub

    def call_id(m):
        alphabet = string.ascii_letters + string.digits
        return "toolu_01" + "".join(alphabet[b % len(alphabet)] for b in hashlib.sha256(m.group(0).encode()).digest()[:22])

    def clean(text):
        for real, fake in sorted(swaps.items(), key=lambda kv: -len(kv[0])):
            text = text.replace(real, fake)
        text = POINTS.sub(count(points), text)
        text = COMMENTS.sub(count(comments), text)
        # Item IDs only appear in command output, and elsewhere eight digits may be part of an ID of the app's own
        if text.startswith("exit code"):
            text = ITEM_ID.sub(lambda m: str(41_000_000 + stable(m.group(0), 999_999)), text)
        # The scripted model's call IDs and the test user would show on the Raw tab and in the header
        text = CALL_ID.sub(call_id, text)
        text = HEX_ID.sub(lambda m: hashlib.sha256(m.group(0).encode()).hexdigest(), text)
        for real, fake in PEOPLE:
            text = text.replace(real, fake)
        return text

    return clean


def recording(a, run_id, with_version):
    run = a.get(f"/api/runs/{run_id}")
    version = None
    if with_version and run["reflectionVersion"]:
        version = a.get(f"/api/jobs/{run['jobId']}/playbook/versions/{run['reflectionVersion']}")
    return {"run": run, "events": a.get(f"/api/runs/{run_id}/events?limit=2000"), "artifacts": a.get(f"/api/runs/{run_id}/artifacts"), "version": version}


a = api()
runs = {r["number"]: r for r in a.get("/api/runs?search=Hacker%20News%20digest&pageSize=100")["items"] if r["jobName"] == "Hacker News digest"}
data = {
    "user": a.get("/api/users/me"),
    "explore": recording(a, runs[EXPLORE_RUN]["id"], True),
    "scripted": recording(a, runs[SCRIPTED_RUN]["id"], False),
}
data = rewrite(data, cleaner(data))

# The demo shows the first run as the scheduled run it would be, signed in as a member, whom the app doesn't offer deleting runs
data["explore"]["run"].update({"trigger": "schedule", "triggeredBy": None, "triggeredByName": None})
data["user"]["workspace"].update({"role": "member", "name": "Acme"})


def ts(value, indent=1):
    return json.dumps(value, indent="\t", ensure_ascii=False).replace("\n", "\n" + "\t" * indent)


def recorded(key):
    r = data[key]
    return f"{{\n\trun: {ts(r['run'])},\n\tevents: {ts(r['events'])},\n\tartifacts: {ts(r['artifacts'])},\n\tversion: {ts(r['version'])}\n}}"


with open(OUT, "w") as f:
    f.write(f"""// Two runs of the Hacker News digest, recorded from a real instance by .claude/skills/update-docs/scripts/screenshots/demo.py
// The live front page they fetched is replaced with fictional stories, and the scripted model's call IDs and the test user with realistic ones
// Timestamps are those of the recording, the fake server moves them to the moment a run plays
import type {{
	PlaybookVersion,
	RunArtifact,
	RunDetail,
	RunEvent,
	User
}} from '$lib/api/types';

export type RecordedRun = {{
	run: RunDetail;
	events: RunEvent[];
	artifacts: RunArtifact[];
	// The playbook version the run's reflection created, for the Learned tab's diff
	version: PlaybookVersion | null;
}};

export const user = {ts(data['user'], 0)} satisfies User;

// The first run explores the APIs with the model and writes the scripts the job keeps
export const exploreRun: RecordedRun = {recorded('explore')};

// After graduating the job runs its main script without the model
export const scriptedRun: RecordedRun = {recorded('scripted')};
""")
subprocess.run(["pnpm", "exec", "prettier", "--write", OUT], cwd=FRONTEND, check=True, capture_output=True)
print(f"wrote {OUT}")
