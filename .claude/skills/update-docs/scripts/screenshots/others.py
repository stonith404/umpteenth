"""The other jobs of the workspace: a stale PR digest, a dependency report, release notes and an uptime check."""
import json
import os
import sys

from lib import HERE, api, at, finish, load_state, op, reflection, run_one, scale, turn, u

a = api()
state = load_state()
jobs = state["ids"]["jobs"]
which = sys.argv[1:]


def read(name):
    with open(os.path.join(HERE, "pb", name)) as f:
        return f.read()


nothing = lambda cost_in=9000, out=300: reflection("Nothing new: the run followed the playbook.", [], usage=u(cost_in, out), delay=2500)

# Stale PR digest: learns a few things, stays Assisted because it posts to Slack
if "pr" in which:
    runs = [
        (14, 0.31, "Posted 5 stale pull requests to #eng: 2 by @mkoch, 2 by @jlee and 1 by @sam-r. The oldest is acme/api#1482 (retry budget for outgoing webhooks), quiet for 11 days.", {"stale": 5, "posted": True}, "success"),
        (15, 0.24, "Posted 4 stale pull requests to #eng. acme/web#977 got a review yesterday and dropped off the list.", {"stale": 4, "posted": True}, "success"),
        (16, 0.21, "Posted 4 stale pull requests to #eng from 3 authors. Nothing changed since yesterday.", {"stale": 4, "posted": True}, "success"),
        (17, 0.19, "Posted 3 stale pull requests to #eng. acme/api#1482 was merged overnight.", {"stale": 3, "posted": True}, "success"),
        (18, 0.18, "No pull request has been quiet for 3 days, so nothing was posted.", {"stale": 0, "posted": False}, "success"),
        (21, 0.12, "GitHub answered 502 Bad Gateway three times while listing the pull requests of acme/web, so nothing was posted. acme/api had 2 stale pull requests.", {"stale": 2, "posted": False}, "failure"),
        (22, 0.17, "Posted 3 stale pull requests to #eng: 2 in acme/api and 1 in acme/web.", {"stale": 3, "posted": True}, "success"),
        (23, 0.16, "Posted 2 stale pull requests to #eng, both by @jlee.", {"stale": 2, "posted": True}, "success"),
        (24, 0.17, "Posted 3 stale pull requests to #eng. acme/web#1011 has waited 4 days for a review from @ada.", {"stale": 3, "posted": True}, "success"),
        (25, 0.15, "Posted 2 stale pull requests to #eng. Both have been quiet for exactly 3 days.", {"stale": 2, "posted": True}, "success"),
    ]
    last = "(not set)"
    for i, (day, usd, summary, outputs, status) in enumerate(runs, start=1):
        turns = [
            turn(text="I'll check what the last digest reported, so the message can say what changed.", calls=[("state_get", {"key": "last_digest"})], usage=u(4100, 220, 0, 11200), delay=3100),
            turn(text="Pull requests without activity since this cutoff count as stale.", calls=[("bash", {"command": "date -u -d '3 days ago' +%FT%TZ"})], usage=u(900, 160, 11200, 1400), delay=2400),
            turn(calls=[("state_set", {"key": "last_digest", "value": json.dumps({"stale": outputs["stale"], "posted": outputs["posted"]})})], usage=u(2600, 380, 12600, 2900), delay=2800),
            finish(summary, outputs, status=status, usage=u(1200, 520, 15500, 900), delay=3300),
        ]
        scale(turns, usd, "sonnet")
        if i == 1:
            refl = reflection("Wrote down how to find stale pull requests and how the team wants the message.", [
                op("add_learning", kind="fact", when="finding stale pull requests", text="Use the updated date of a pull request, not its last commit: a review or comment also counts as activity.", rationale="The instruction talks about activity, not commits."),
                op("add_learning", kind="preference", when="writing the message", text="Group by author with an @mention and list each pull request as repo#number with its title. Keep the message under 15 lines.", rationale="The first message was long."),
            ], usage=u(15200, 1400), delay=5200)
        elif i == 6:
            refl = reflection("Added a retry rule for GitHub's 502 answers on large repositories.", [
                op("add_learning", kind="workaround", when="GitHub answers 502", text="GitHub answers 502 when a search covers a large repository. Query acme/web page by page with 30 results per page instead of one search.", rationale="The run failed on the third 502 in a row."),
            ], used=["L1"], usage=u(13800, 900), delay=4300)
        else:
            refl = nothing(12000, 280)
        run_one(a, state, jobs["pr"], "schedule", at(day, "09:00", 1), turns + [refl], label=f"pr-{i}")

# Weekly dependency report: two Monday runs with real registry lookups
if "deps" in which:
    report1 = """# Dependency report

## Security advisories
- golang.org/x/net v0.38.0 → v0.41.0 (acme/api): GO-2026-3811, HTTP/2 stream reset handling

## Major updates
- react-router 6.30.1 → 7.9.2 (acme/web)
- @tanstack/react-query 4.36.1 → 5.90.2 (acme/web)

## Minor updates
- github.com/jackc/pgx/v5 5.6.0 → 5.7.6 (acme/api)
- github.com/go-chi/chi/v5 5.1.0 → 5.2.3 (acme/api)
- vite 6.2.0 → 6.3.6 (acme/web)
- zod 3.23.8 → 3.25.76 (acme/web)

## Patch updates
- github.com/stretchr/testify 1.10.0 → 1.10.1 (acme/api)
- typescript 5.9.2 → 5.9.3 (acme/web)
"""
    report2 = report1.replace("## Security advisories\n- golang.org/x/net v0.38.0 → v0.41.0 (acme/api): GO-2026-3811, HTTP/2 stream reset handling\n\n", "## Security advisories\nNone this week.\n\n")
    for i, (day, usd, report, summary, outputs, mode) in enumerate([
        (14, 0.58, report1, "Found 12 outdated dependencies across acme/api and acme/web: 2 major, 6 minor and 4 patch updates. golang.org/x/net has a security advisory and is at the top of `dependency-report.md`.", {"outdated": 12, "advisories": 1}, "explore"),
        (21, 0.34, report2, "Found 9 outdated dependencies across acme/api and acme/web: 2 major, 4 minor and 3 patch updates. No security advisories this week. The report is in `dependency-report.md`.", {"outdated": 9, "advisories": 0}, "assisted"),
    ], start=1):
        turns = [
            turn(text="Let me check the latest versions on the Go module proxy and the npm registry.",
                 calls=[("bash", {"command": "curl -s https://proxy.golang.org/github.com/jackc/pgx/v5/@latest | jq -c '{Version, Time}' && npm view react-router version"})],
                 usage=u(4800, 330, 0, 12400), delay=4200),
            turn(text="I have every version I need. Writing the report with the advisory first.",
                 calls=[("write_file", {"path": "/ump/outputs/dependency-report.md", "content": report})],
                 usage=u(6200, 1900, 12400, 6800), delay=7400),
            finish(summary, outputs, usage=u(1100, 510, 19200, 800), delay=3200),
        ]
        scale(turns, usd, "sonnet")
        if i == 1:
            refl = reflection("Noted where to look up versions and advisories.", [
                op("add_learning", kind="fact", when="looking up versions", text="The Go module proxy answers /@latest for every module, and npm view <package> version works without a token. Neither needs the GitHub server.", rationale="Both lookups worked without credentials."),
                op("add_learning", kind="fact", when="checking advisories", text="Look up Go advisories with govulncheck's database and npm advisories with npm audit --json, then list them before the updates.", rationale="The report has to start with advisories."),
            ], usage=u(16400, 1300), delay=4800)
        else:
            refl = nothing(12500, 260)
        run_one(a, state, jobs["deps"], "schedule", at(day, "09:30", 1), turns + [refl], label=f"deps-{i}")

# Release notes: started by CI through the job's webhook
if "release" in which:
    releases = [
        (16, "15:42", 0.27, "v1.14.0", ["Export invoices as PDF by @mkoch in https://github.com/acme/api/pull/1490", "Custom signing secrets for outgoing webhooks by @jlee in https://github.com/acme/api/pull/1493", "Refactor the billing repository layer by @sam-r in https://github.com/acme/api/pull/1495", "Faster invoice search on large accounts by @mkoch in https://github.com/acme/api/pull/1497", "Fix duplicate reminder emails after a restart by @jlee in https://github.com/acme/api/pull/1501", "Fix time zones with half-hour offsets in due dates by @ada in https://github.com/acme/api/pull/1502"],
         "## New\n- Export any invoice as a PDF.\n- Outgoing webhooks can use your own signing secret.\n\n## Improved\n- Invoice search is faster on accounts with many invoices.\n\n## Fixed\n- Reminder emails no longer go out twice after maintenance.\n- Due dates are right in time zones with half-hour offsets.",
         "Drafted the notes for v1.14.0: 2 new features, 1 improvement and 2 fixes. Left out the billing refactor, which customers don't notice."),
        (24, "11:05", 0.15, "v1.15.0", ["Bulk-edit customer tags by @sam-r in https://github.com/acme/api/pull/1512", "Retry budget for outgoing webhooks by @mkoch in https://github.com/acme/api/pull/1482", "Bump pgx to 5.7.6 by @jlee in https://github.com/acme/api/pull/1515", "Fix CSV export for amounts over one million by @ada in https://github.com/acme/api/pull/1518"],
         "## New\n- Edit the tags of many customers at once.\n\n## Improved\n- Outgoing webhooks retry within a budget instead of giving up after one failure.\n\n## Fixed\n- CSV exports show amounts over one million correctly.",
         "Drafted the notes for v1.15.0: 1 new feature, 1 improvement and 1 fix. Left out the pgx update."),
    ]
    for i, (day, hhmm, usd, tag, prs, notes, summary) in enumerate(releases, start=1):
        payload = {"action": "published", "release": {"tag_name": tag, "name": tag, "html_url": f"https://github.com/acme/api/releases/tag/{tag}", "body": "## What's Changed\n" + "\n".join("* " + p for p in prs)}, "repository": {"full_name": "acme/api"}}
        turns = [
            turn(text="The release payload is in the run input. Let me read the tag and the changes.",
                 calls=[("bash", {"command": "jq -r '.release.tag_name, .release.body' /ump/input.json"})],
                 usage=u(4400, 260, 0, 11600), delay=3300),
            turn(text="The refactors and dependency bumps stay out, the rest goes into New, Improved and Fixed.",
                 reasoning="Customers care about what changed for them. Refactors and version bumps of libraries don't change anything they see.",
                 calls=[("finish", {"status": "success", "summary": summary, "outputs": {"notes": notes}})],
                 usage=u(1600, 900, 11600, 1500), delay=6900),
        ]
        scale(turns, usd, "sonnet")
        if i == 1:
            refl = reflection("Wrote down which changes stay out of customer notes.", [
                op("add_learning", kind="preference", when="picking changes", text="Leave out refactors, dependency bumps and CI changes. Everything a customer can see goes into New, Improved or Fixed.", rationale="The run had to decide this from the instruction."),
            ], usage=u(11800, 700), delay=3900)
        else:
            refl = nothing(10500, 240)
        run_one(a, state, jobs["release"], "webhook", at(day, hhmm, 3), turns + [refl], label=f"release-{i}", body=payload, token=state["ids"]["releaseToken"])

# Uptime check: explores once, runs the toolkit script three times, then graduates and runs without the model
if "uptime" in which:
    CHECK = read("check_sites")
    MAIN = read("main_uptime")
    sites = "https://example.com https://example.org"
    days = [14, 15, 16, 17, 18, 21, 22, 23, 24, 25]
    slots = []
    for day in days:
        hours = ["08:41", "10:00", "12:00", "14:00", "16:00", "18:00"] if day == 14 else ["08:00", "10:00", "12:00", "14:00", "16:00", "18:00"]
        slots += [(day, h) for h in hours]
    start = int(sys.argv[sys.argv.index("uptime") + 1]) if len(sys.argv) > sys.argv.index("uptime") + 1 else 0
    for n, (day, hhmm) in enumerate(slots, start=1):
        if n <= start:
            continue
        how = "manual" if n == 1 else "schedule"
        if n == 1:
            turns = [
                turn(text="I'll check both sites with curl and time the answers.",
                     calls=[("bash", {"command": "for site in https://example.com https://example.org; do curl -s -o /dev/null -w \"$site %{http_code} %{time_total}s\\n\" --max-time 5 \"$site\"; done"})],
                     usage=u(3900, 240, 0, 9800), delay=1800),
                turn(text="Both answered 200 well within 2 seconds. This is the first run, so I'll save the statuses.",
                     calls=[("state_set", {"key": "last_status", "value": json.dumps({"https://example.com": "up", "https://example.org": "up"})})],
                     usage=u(700, 180, 9800, 900), delay=1500),
                finish("Both sites answered 200 within 2 seconds. This was the first check, so both statuses were saved.", {"up": 2, "changed": False}, usage=u(500, 220, 10700, 400), delay=1400),
            ]
            refl = reflection("Kept the check as a toolkit script that also compares with the last status.", [
                op("upsert_script", name="check_sites", content=CHECK, rationale="Every run does the same check and state comparison."),
                op("add_learning", kind="fact", when="checking the sites", text="Both sites answer in about 200 ms, so a timeout of 5 seconds per request leaves room for a slow answer to count as down.", rationale="The run measured the answers."),
            ], usage=u(9800, 1100), delay=2600)
            resp = turns + [refl]
        elif n <= 4:
            turns = [
                turn(text="Running the check from the playbook.", calls=[("toolkit__check_sites", {"sites": sites})], usage=u(3600, 150, 0, 10100), delay=1300),
                finish("Both sites answered 200 within 2 seconds. No status changed since the last check.", {"up": 2, "changed": False}, usage=u(600, 160, 10100, 300), delay=1100),
            ]
            if n == 4:
                verify = json.dumps({"checks": ["exit_code == 0", "output.up exists", "output.changed exists"], "llm": False})
                refl = reflection("Graduated: three runs in a row only called check_sites, so a main script now runs the check without the model.", [
                    op("propose_main", content=MAIN, rationale="Runs #2 to #4 called check_sites and finished."),
                    op("set_verify", content=verify, rationale="A scripted run must finish cleanly and report both outputs."),
                ], used=["L1"], usage=u(10200, 900), delay=2400)
            else:
                refl = reflection("Nothing new: the check ran as the playbook says.", [], used=["L1"], usage=u(8100, 160), delay=1500)
            resp = turns + [refl]
        else:
            resp = []
        run_one(a, state, jobs["uptime"], how, at(day, hhmm, 2), resp, label=f"uptime-{n}")
