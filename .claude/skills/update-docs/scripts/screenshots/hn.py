"""The centerpiece: the Hacker News digest goes from Explore through Assisted to Scripted."""
import json
import os
import subprocess
import sys

from lib import (HERE, api, at, finish, load_state, op, reflection, run_one, save_state, scale, turn, u)

a = api()
state = load_state()
job = state["ids"]["jobs"]["hn"]
PB = os.path.join(HERE, "pb")


def read(name):
    with open(os.path.join(PB, name)) as f:
        return f.read()


FETCH = read("fetch_front_page")
RENDER = read("render_digest")
MAIN = read("main_hn")


def body(script):
    """The script as the agent writes it while exploring, without the ump: header."""
    return "\n".join(line for line in script.splitlines() if not line.startswith("# ump:")) + "\n"


def front_page():
    """Runs the same scripts on this machine, so the scripted finish matches what the sandbox finds seconds later."""
    tmp = os.path.join(HERE, "tmp")
    os.makedirs(tmp, exist_ok=True)
    subprocess.run(["python3", os.path.join(PB, "fetch_front_page"), "--limit", "30", "--out", os.path.join(tmp, "front.json")], check=True, capture_output=True)
    out = subprocess.run(["python3", os.path.join(PB, "render_digest"), "--input", os.path.join(tmp, "front.json"), "--output", os.path.join(tmp, "digest.md")], check=True, capture_output=True, text=True).stdout
    result = json.loads(out)
    with open(os.path.join(tmp, "front.json")) as f:
        stories = json.load(f)
    return result["stories"], result["matches"], stories


def summary_for(n, m, stories):
    top = stories[0]
    noun = "story" if m == 1 else "stories"
    return f"Wrote the digest of {n} front-page stories to `digest.md` and marked {m} {noun} about self-hosting, Go or databases with a ★. The top story is \"{top['title']}\" with {top['points']} points."


ALGOLIA = "https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=30"
which = sys.argv[1:] or ["1", "2", "3", "4", "5", "6"]

# Run 1 explores the APIs, writes both scripts by hand and reflection keeps the fetcher, three learnings and holds a new base image back
if "1" in which:
    n, m, stories = front_page()
    turns = [
        turn(text="Let me see what the official Hacker News API returns for the front page.",
             reasoning="The digest needs the title, link, points and comment count of every front-page story. I'll start with the official API and check what it gives me.",
             calls=[("bash", {"command": "curl -s https://hacker-news.firebaseio.com/v0/topstories.json | jq '.[0:5]'"})],
             usage=u(3400, 310, 0, 9200), delay=5200),
        turn(text="Those are only IDs. Let me look at one item.",
             calls=[("bash", {"command": "curl -s \"https://hacker-news.firebaseio.com/v0/item/$(curl -s https://hacker-news.firebaseio.com/v0/topstories.json | jq '.[0]').json\" | jq '{title, url, score, descendants}'"})],
             usage=u(800, 260, 9200, 1100), delay=3900),
        turn(text="The official API needs one request per story, 31 for the whole front page. The Algolia search API has a front_page tag that should return everything in one call.",
             reasoning="Algolia's HN search indexes the front page with the front_page tag and includes points and num_comments on each hit.",
             calls=[("bash", {"command": f"curl -s '{ALGOLIA}' | jq -c '.hits[0:3][] | {{title, url, points, num_comments}}'"})],
             usage=u(900, 420, 10300, 1300), delay=6100),
        turn(text="One request gives me title, URL, points and comments. Ask HN threads have no URL, let me check if any are on the front page today.",
             calls=[("bash", {"command": f"curl -s '{ALGOLIA}' | jq '[.hits[] | select(.url == null) | .title]'"})],
             usage=u(7800, 230, 11600, 7900), delay=4400),
        turn(text="None today, but the fetcher should still link URL-less posts to their comments page. I'll write it.",
             calls=[("write_file", {"path": "/workspace/fetch_front_page.py", "content": body(FETCH)})],
             usage=u(600, 1700, 19500, 900), delay=8300),
        turn(calls=[("bash", {"command": "python3 /workspace/fetch_front_page.py --limit 30 --out /workspace/front.json && jq -c '.[0:2][]' /workspace/front.json"})],
             usage=u(1100, 170, 21100, 2300), delay=3100),
        turn(text="Now the digest itself: sorted by points, with a ★ for self-hosting, Go and databases. \"Go\" only counts with a capital G, otherwise every \"go\" in a title matches.",
             calls=[("write_file", {"path": "/workspace/render_digest.py", "content": body(RENDER)})],
             usage=u(900, 2300, 23400, 1400), delay=8800),
        turn(calls=[("bash", {"command": "python3 /workspace/render_digest.py --input /workspace/front.json --output /ump/outputs/digest.md && head -n 7 /ump/outputs/digest.md"})],
             usage=u(1500, 190, 26200, 2600), delay=3300),
        turn(calls=[("remember", {"note": "Ask HN threads come without a URL. The fetcher links them to their comments page."})],
             usage=u(1300, 150, 28800, 1500), delay=2600),
        finish(summary_for(n, m, stories), {"stories": n, "matches": m}, usage=u(500, 480, 30300, 700), delay=3700),
    ]
    refl = reflection(
        "Kept the front-page fetcher as a toolkit script and wrote down which API to use, where Ask HN links go and how topics match.",
        [
            op("add_learning", kind="fact", when="fetching the front page",
               text="The Algolia search API with tags=front_page returns title, URL, points and comment count in one request. The official Firebase API needs one request per story.",
               rationale="The run spent two turns finding the right API."),
            op("add_learning", kind="edge_case", when="building links",
               text="Ask HN threads have no URL. Link them to their comments page on news.ycombinator.com instead of leaving the link empty.",
               rationale="The agent flagged it with remember."),
            op("add_learning", kind="preference", when="marking topics",
               text="Match \"Go\" only with a capital G or as \"Golang\". Lowercase \"go\" appears in too many titles.",
               rationale="The run chose this rule on purpose and the next run should keep it."),
            op("upsert_script", name="fetch_front_page", content=FETCH,
               rationale="The fetcher worked on the first try and every run needs it."),
            op("set_dockerfile", content="FROM python:3.13-slim\nRUN apt-get update && apt-get install -y --no-install-recommends curl jq && rm -rf /var/lib/apt/lists/*",
               rationale="A slim Python image would start faster than the default sandbox image."),
        ],
        usage=u(14800, 2900),
        delay=9500,
    )
    run_one(a, state, job, "manual", at(14, "10:04", 12), turns + [refl], label="hn-1")

# Run 2 uses the fetcher, writes the renderer again, and reflection keeps it too
if "2" in which:
    n, m, stories = front_page()
    turns = [
        turn(text="The playbook has a fetcher for the front page. I'll use it, then write the digest.",
             calls=[("toolkit__fetch_front_page", {"limit": 30, "out": "/workspace/front.json"})],
             usage=u(3900, 210, 0, 10400), delay=4300),
        turn(text="The toolkit has no renderer yet, so I'll write one that sorts by points and marks our topics.",
             calls=[("write_file", {"path": "/workspace/render_digest.py", "content": body(RENDER)})],
             usage=u(700, 2200, 10400, 900), delay=7600),
        turn(calls=[("bash", {"command": "python3 /workspace/render_digest.py --input /workspace/front.json --output /ump/outputs/digest.md && head -n 7 /ump/outputs/digest.md"})],
             usage=u(900, 180, 11300, 2500), delay=2900),
        finish(summary_for(n, m, stories), {"stories": n, "matches": m}, usage=u(1400, 450, 13800, 600), delay=3400),
    ]
    refl = reflection(
        "Kept the renderer as a toolkit script, so the next run only calls two tools.",
        [
            op("upsert_script", name="render_digest", content=RENDER,
               rationale="The run wrote the same renderer as run #1. As a tool the next run can skip writing it."),
            op("add_learning", kind="workflow", when="every run",
               text="Call fetch_front_page with --out /workspace/front.json, then render_digest into /ump/outputs/digest.md. Nothing else is needed.",
               rationale="Both steps worked without changes."),
        ],
        used=["L1", "L2", "L3"],
        usage=u(16000, 2400),
        delay=7800,
    )
    run_one(a, state, job, "schedule", at(15, "07:30", 1), turns + [refl], label="hn-2")


def assisted(number, target, refl):
    n, m, stories = front_page()
    turns = [
        turn(text="Following the playbook: fetch the front page, then render the digest.",
             calls=[("toolkit__fetch_front_page", {"limit": 30, "out": "/workspace/front.json"})],
             usage=u(4300, 190, 0, 10900), delay=3800),
        turn(calls=[("toolkit__render_digest", {"input": "/workspace/front.json", "output": "/ump/outputs/digest.md"})],
             usage=u(300, 120, 10900, 500), delay=2100),
        turn(text="Let me check the top of the digest before I finish.",
             calls=[("bash", {"command": "head -n 7 /ump/outputs/digest.md"})],
             usage=u(250, 90, 11400, 300), delay=1900),
        finish(summary_for(n, m, stories), {"stories": n, "matches": m}, usage=u(900, 380, 11700, 400), delay=3000),
    ]
    return run_one(a, state, job, "schedule", target, turns + [refl], label=f"hn-{number}")


nothing = lambda: reflection("Nothing new: the run followed the playbook and both tools worked.", [], used=["L1", "L3", "L4"], usage=u(12000, 350), delay=4200)

if "3" in which:
    assisted(3, at(16, "07:30", 1), nothing())
if "4" in which:
    assisted(4, at(17, "07:30", 1), nothing())

# Run 5 is the third Assisted run in a row with the same two tool calls, so reflection graduates the job
if "5" in which:
    verify = json.dumps({"checks": ["exit_code == 0", "output.stories == 30", "file /ump/outputs/digest.md exists"], "llm": False, "varies": ["matches"]})
    refl = reflection(
        "Graduated: three runs in a row made the same two tool calls, so a main script now writes the digest without the agent.",
        [
            op("propose_main", content=MAIN, rationale="Runs #3 to #5 called fetch_front_page and render_digest and nothing else."),
            op("set_verify", content=verify, rationale="A scripted run must produce all 30 stories and the digest file. The number of matches changes with the front page."),
        ],
        used=["L1", "L3", "L4"],
        usage=u(15200, 1900),
        delay=8200,
    )
    assisted(5, at(18, "07:30", 1), refl)

# Scripted runs need no model at all
if "6" in which:
    days = [int(d) for d in (sys.argv[2:] if len(sys.argv) > 2 else [])] or [21, 22, 23, 24, 25]
    for day in days:
        run_one(a, state, job, "schedule", at(day, "07:30", 1), [], label=f"hn-scripted-{day}")
