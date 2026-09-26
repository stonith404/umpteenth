// Two runs of the Hacker News digest, recorded from a real instance by .claude/skills/update-docs/scripts/screenshots/demo.py
// The live front page they fetched is replaced with fictional stories, and the scripted model's call IDs and the test user with realistic ones
// Timestamps are those of the recording, the fake server moves them to the moment a run plays
import type { PlaybookVersion, RunArtifact, RunDetail, RunEvent, User } from '$lib/api/types';

export type RecordedRun = {
	run: RunDetail;
	events: RunEvent[];
	artifacts: RunArtifact[];
	// The playbook version the run's reflection created, for the Learned tab's diff
	version: PlaybookVersion | null;
};

export const user = {
	id: '01a0e9c1-063f-710e-80af-ea69fc08f571',
	email: 'ada@example.com',
	name: 'Ada Park',
	picture: null,
	workspaceId: '00000000-0000-7000-8000-000000000001',
	viaToken: false,
	isAdmin: false,
	workspacesEnabled: false,
	workspace: {
		id: '00000000-0000-7000-8000-000000000001',
		name: 'Acme',
		role: 'member',
		usageUnit: 'price'
	}
} satisfies User;

// The first run explores the APIs with the model and writes the scripts the job keeps
export const exploreRun: RecordedRun = {
	run: {
		id: '01a0e9c1-1e5d-75db-9e97-7e6573a7046d',
		jobId: '01a0e9c1-0719-72c0-abb4-03f2b1fc1ffb',
		jobName: 'Hacker News digest',
		number: 1,
		status: 'succeeded',
		mode: 'explore',
		trigger: 'schedule',
		queuedAt: 1790628142685,
		startedAt: 1790628142687,
		finishedAt: 1790628196153,
		msQueue: 2,
		msProvision: 415,
		msLlm: 49551,
		msTools: 3170,
		msTotal: 53465,
		turns: 10,
		tokIn: 18800,
		tokOut: 6210,
		tokCacheRead: 180400,
		tokCacheWrite: 28900,
		cost: 208030,
		modelName: 'claude-sonnet-5',
		modelLabel: 'Claude Sonnet 5',
		summary:
			'Wrote the digest of 30 front-page stories to `digest.md` and marked 1 story about self-hosting, Go or databases with a ★. The top story is "The quiet joy of cron" with 331 points.',
		error: null,
		sandboxIsolation: 'container',
		input: null,
		instructions: null,
		outputs: {
			matches: 1,
			stories: 30
		},
		playbookVersion: 0,
		imageRef: 'ghcr.io/stonith404/umpteenth-sandbox:latest',
		sandboxAdapter: 'docker',
		triggeredBy: null,
		triggeredByName: null,
		jobDeleted: false,
		fellBack: false,
		cancelRequested: false,
		reflection: 'done',
		reflectionError: null,
		reflectionSummary:
			'Kept the front-page fetcher as a toolkit script and wrote down which API to use, where Ask HN links go and how topics match.',
		reflectionOps: [
			{
				op: 'add_learning',
				rationale: 'The run spent two turns finding the right API.',
				id: null,
				kind: 'fact',
				text: 'The Algolia search API with tags=front_page returns title, URL, points and comment count in one request. The official Firebase API needs one request per story.',
				when: 'fetching the front page',
				name: null,
				content: null,
				status: 'applied',
				target: 'L1'
			},
			{
				op: 'add_learning',
				rationale: 'The agent flagged it with remember.',
				id: null,
				kind: 'edge_case',
				text: 'Ask HN threads have no URL. Link them to their comments page on news.ycombinator.com instead of leaving the link empty.',
				when: 'building links',
				name: null,
				content: null,
				status: 'applied',
				target: 'L2'
			},
			{
				op: 'add_learning',
				rationale: 'The run chose this rule on purpose and the next run should keep it.',
				id: null,
				kind: 'preference',
				text: 'Match "Go" only with a capital G or as "Golang". Lowercase "go" appears in too many titles.',
				when: 'marking topics',
				name: null,
				content: null,
				status: 'applied',
				target: 'L3'
			},
			{
				op: 'upsert_script',
				rationale: 'The fetcher worked on the first try and every run needs it.',
				id: null,
				kind: null,
				text: null,
				when: null,
				name: 'fetch_front_page',
				content:
					'#!/usr/bin/env python3\n# ump:name        fetch_front_page\n# ump:description Fetch the Hacker News front page and save the stories as JSON, sorted by points\n# ump:args        {"limit":"integer","out":"string"}\n# ump:side-effects none\nimport argparse\nimport json\nimport urllib.request\n\nparser = argparse.ArgumentParser()\nparser.add_argument("--limit", type=int, default=30)\nparser.add_argument("--out", required=True)\nargs = parser.parse_args()\n\n# The Algolia API returns points and comment counts in one request, the Firebase API needs one request per story\nurl = f"https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage={args.limit}"\nwith urllib.request.urlopen(url, timeout=20) as response:\n    hits = json.load(response)["hits"]\n\n# Ask HN and Show HN posts have no URL, so they link to their comments page\nstories = [\n    {\n        "id": int(hit["objectID"]),\n        "title": hit["title"],\n        "url": hit.get("url") or f"https://news.ycombinator.com/item?id={hit[\'objectID\']}",\n        "points": hit.get("points") or 0,\n        "comments": hit.get("num_comments") or 0,\n    }\n    for hit in hits\n]\nstories.sort(key=lambda story: story["points"], reverse=True)\n\nwith open(args.out, "w") as f:\n    json.dump(stories, f, indent=2)\nprint(f"Saved {len(stories)} stories to {args.out}")\n',
				status: 'applied',
				target: 'fetch_front_page'
			},
			{
				op: 'set_dockerfile',
				rationale: 'A slim Python image would start faster than the default sandbox image.',
				id: null,
				kind: null,
				text: null,
				when: null,
				name: null,
				content:
					'FROM python:3.13-slim\nRUN apt-get update && apt-get install -y --no-install-recommends curl jq && rm -rf /var/lib/apt/lists/*',
				status: 'held',
				target: 'dockerfile',
				flags: ['builds on a new base image python:3.13-slim']
			}
		],
		reflectionVersion: 1,
		reflectionCost: 58600,
		reflectionTokens: 17700,
		verifyCost: 0,
		verifyTokens: 0
	},
	events: [
		{
			seq: 1,
			ts: 1790628142687,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'provisioning'
			}
		},
		{
			seq: 2,
			ts: 1790628143102,
			type: 'sandbox.create',
			spanId: '01a0e9c1-1e60-71ae-9d9c-2f9bc9802208',
			parentSpanId: null,
			ms: 414,
			payload: {
				adapter: 'docker',
				image: 'ghcr.io/stonith404/umpteenth-sandbox:latest',
				isolation: 'container',
				sandboxId: '39b29b796ce9441965df4cb8cb4cf4f2e12d564cefd7ae4b2d343a9c77d2141a'
			}
		},
		{
			seq: 3,
			ts: 1790628143124,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'running'
			}
		},
		{
			seq: 4,
			ts: 1790628148338,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 5213,
			payload: {
				cost: 32900,
				latencyMs: 5213,
				model: 'claude-sonnet-5',
				reasoning:
					"The digest needs the title, link, points and comment count of every front-page story. I'll start with the official API and check what it gives me.",
				stop: 'tool_use',
				text: 'Let me see what the official Hacker News API returns for the front page.',
				toolCalls: [
					{
						id: 'toolu_010Lx6a4kcK20mg7EhbPstBt',
						name: 'bash'
					}
				],
				turn: 1,
				usage: {
					input: 3400,
					output: 310,
					cacheRead: 0,
					cacheWrite: 9200,
					reasoning: 0
				}
			}
		},
		{
			seq: 5,
			ts: 1790628148339,
			type: 'tool.call',
			spanId: '01a0e9c1-3473-79eb-8cd9-7b16e8c6bb41',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command: "curl -s https://hacker-news.firebaseio.com/v0/topstories.json | jq '.[0:5]'"
				},
				callId: 'toolu_010Lx6a4kcK20mg7EhbPstBt',
				name: 'bash'
			}
		},
		{
			seq: 6,
			ts: 1790628148583,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				endpoint: 'proxy hacker-news.firebaseio.com',
				ok: true
			}
		},
		{
			seq: 7,
			ts: 1790628148954,
			type: 'tool.result',
			spanId: '01a0e9c1-3473-79eb-8cd9-7b16e8c6bb41',
			parentSpanId: null,
			ms: 615,
			payload: {
				callId: 'toolu_010Lx6a4kcK20mg7EhbPstBt',
				content:
					'exit code: 0\n[\n  41105063,\n  41434734,\n  41573962,\n  41835669,\n  41835027\n]\n',
				isError: false,
				meta: {
					bytes: 63,
					exitCode: 0,
					logPath: '/ump/logs/toolu_010Lx6a4kcK20mg7EhbPstBt.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 8,
			ts: 1790628152868,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 3912,
			payload: {
				cost: 8790,
				latencyMs: 3912,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: 'Those are only IDs. Let me look at one item.',
				toolCalls: [
					{
						id: 'toolu_013sTZJSohcKoGMohDAfWZ9I',
						name: 'bash'
					}
				],
				turn: 2,
				usage: {
					input: 800,
					output: 260,
					cacheRead: 9200,
					cacheWrite: 1100,
					reasoning: 0
				}
			}
		},
		{
			seq: 9,
			ts: 1790628152868,
			type: 'tool.call',
			spanId: '01a0e9c1-4624-797c-b7d9-4152fffd4f5c',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command:
						"curl -s \"https://hacker-news.firebaseio.com/v0/item/$(curl -s https://hacker-news.firebaseio.com/v0/topstories.json | jq '.[0]').json\" | jq '{title, url, score, descendants}'"
				},
				callId: 'toolu_013sTZJSohcKoGMohDAfWZ9I',
				name: 'bash'
			}
		},
		{
			seq: 10,
			ts: 1790628153582,
			type: 'tool.result',
			spanId: '01a0e9c1-4624-797c-b7d9-4152fffd4f5c',
			parentSpanId: null,
			ms: 713,
			payload: {
				callId: 'toolu_013sTZJSohcKoGMohDAfWZ9I',
				content:
					'exit code: 0\n{\n  "title": "Show HN: A 400-line RSS reader for a Raspberry Pi",\n  "url": "https://blog.example.dev/rss-pi",\n  "score": 187,\n  "descendants": 128\n}\n',
				isError: false,
				meta: {
					bytes: 144,
					exitCode: 0,
					logPath: '/ump/logs/toolu_013sTZJSohcKoGMohDAfWZ9I.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 11,
			ts: 1790628159713,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 6130,
			payload: {
				cost: 11310,
				latencyMs: 6130,
				model: 'claude-sonnet-5',
				reasoning:
					"Algolia's HN search indexes the front page with the front_page tag and includes points and num_comments on each hit.",
				stop: 'tool_use',
				text: 'The official API needs one request per story, 31 for the whole front page. The Algolia search API has a front_page tag that should return everything in one call.',
				toolCalls: [
					{
						id: 'toolu_01aTqbgRfdidOD83wYj8SFoQ',
						name: 'bash'
					}
				],
				turn: 3,
				usage: {
					input: 900,
					output: 420,
					cacheRead: 10300,
					cacheWrite: 1300,
					reasoning: 0
				}
			}
		},
		{
			seq: 12,
			ts: 1790628159713,
			type: 'tool.call',
			spanId: '01a0e9c1-60e1-79be-b131-883f2b527462',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command:
						"curl -s 'https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=30' | jq -c '.hits[0:3][] | {title, url, points, num_comments}'"
				},
				callId: 'toolu_01aTqbgRfdidOD83wYj8SFoQ',
				name: 'bash'
			}
		},
		{
			seq: 13,
			ts: 1790628159793,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				endpoint: 'proxy hn.algolia.com',
				ok: true
			}
		},
		{
			seq: 14,
			ts: 1790628160189,
			type: 'tool.result',
			spanId: '01a0e9c1-60e1-79be-b131-883f2b527462',
			parentSpanId: null,
			ms: 475,
			payload: {
				callId: 'toolu_01aTqbgRfdidOD83wYj8SFoQ',
				content:
					'exit code: 0\n{"title":"The quiet joy of cron","url":"https://example.net/cron","points":331,"num_comments":22}\n{"title":"Reading your own flame graphs","url":"https://example.com/flame-graphs","points":320,"num_comments":282}\n{"title":"A field guide to retry budgets","url":"https://example.org/retry-budgets","points":223,"num_comments":64}\n',
				isError: false,
				meta: {
					bytes: 330,
					exitCode: 0,
					logPath: '/ump/logs/toolu_01aTqbgRfdidOD83wYj8SFoQ.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 15,
			ts: 1790628164625,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 4435,
			payload: {
				cost: 39970,
				latencyMs: 4435,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: 'One request gives me title, URL, points and comments. Ask HN threads have no URL, let me check if any are on the front page today.',
				toolCalls: [
					{
						id: 'toolu_01YpKzEoO7vqhilxd9drJclV',
						name: 'bash'
					}
				],
				turn: 4,
				usage: {
					input: 7800,
					output: 230,
					cacheRead: 11600,
					cacheWrite: 7900,
					reasoning: 0
				}
			}
		},
		{
			seq: 16,
			ts: 1790628164625,
			type: 'tool.call',
			spanId: '01a0e9c1-7411-7887-a96f-008810923617',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command:
						"curl -s 'https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=30' | jq '[.hits[] | select(.url == null) | .title]'"
				},
				callId: 'toolu_01YpKzEoO7vqhilxd9drJclV',
				name: 'bash'
			}
		},
		{
			seq: 17,
			ts: 1790628165186,
			type: 'tool.result',
			spanId: '01a0e9c1-7411-7887-a96f-008810923617',
			parentSpanId: null,
			ms: 560,
			payload: {
				callId: 'toolu_01YpKzEoO7vqhilxd9drJclV',
				content: 'exit code: 0\n[]\n',
				isError: false,
				meta: {
					bytes: 3,
					exitCode: 0,
					logPath: '/ump/logs/toolu_01YpKzEoO7vqhilxd9drJclV.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 18,
			ts: 1790628173515,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 8328,
			payload: {
				cost: 24350,
				latencyMs: 8328,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: "None today, but the fetcher should still link URL-less posts to their comments page. I'll write it.",
				toolCalls: [
					{
						id: 'toolu_01Phb8bjHhkE4yHjk68S5xG5',
						name: 'write_file'
					}
				],
				turn: 5,
				usage: {
					input: 600,
					output: 1700,
					cacheRead: 19500,
					cacheWrite: 900,
					reasoning: 0
				}
			}
		},
		{
			seq: 19,
			ts: 1790628173515,
			type: 'tool.call',
			spanId: '01a0e9c1-96cb-747e-8965-125b59fca393',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					content:
						'#!/usr/bin/env python3\nimport argparse\nimport json\nimport urllib.request\n\nparser = argparse.ArgumentParser()\nparser.add_argument("--limit", type=int, default=30)\nparser.add_argument("--out", required=True)\nargs = parser.parse_args()\n\n# The Algolia API returns points and comment counts in one request, the Firebase API needs one request per story\nurl = f"https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage={args.limit}"\nwith urllib.request.urlopen(url, timeout=20) as response:\n    hits = json.load(response)["hits"]\n\n# Ask HN and Show HN posts have no URL, so they link to their comments page\nstories = [\n    {\n        "id": int(hit["objectID"]),\n        "title": hit["title"],\n        "url": hit.get("url") or f"https://news.ycombinator.com/item?id={hit[\'objectID\']}",\n        "points": hit.get("points") or 0,\n        "comments": hit.get("num_comments") or 0,\n    }\n    for hit in hits\n]\nstories.sort(key=lambda story: story["points"], reverse=True)\n\nwith open(args.out, "w") as f:\n    json.dump(stories, f, indent=2)\nprint(f"Saved {len(stories)} stories to {args.out}")\n',
					path: '/workspace/fetch_front_page.py'
				},
				callId: 'toolu_01Phb8bjHhkE4yHjk68S5xG5',
				name: 'write_file'
			}
		},
		{
			seq: 20,
			ts: 1790628173565,
			type: 'tool.result',
			spanId: '01a0e9c1-96cb-747e-8965-125b59fca393',
			parentSpanId: null,
			ms: 49,
			payload: {
				callId: 'toolu_01Phb8bjHhkE4yHjk68S5xG5',
				content: 'Wrote 1088 bytes to /workspace/fetch_front_page.py.',
				isError: false,
				meta: null,
				name: 'write_file'
			}
		},
		{
			seq: 21,
			ts: 1790628176666,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 3100,
			payload: {
				cost: 13870,
				latencyMs: 3100,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: '',
				toolCalls: [
					{
						id: 'toolu_01t4AlGiMGHycMHxfgjOoSWX',
						name: 'bash'
					}
				],
				turn: 6,
				usage: {
					input: 1100,
					output: 170,
					cacheRead: 21100,
					cacheWrite: 2300,
					reasoning: 0
				}
			}
		},
		{
			seq: 22,
			ts: 1790628176666,
			type: 'tool.call',
			spanId: '01a0e9c1-a31a-7e18-b367-73824d20b916',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command:
						"python3 /workspace/fetch_front_page.py --limit 30 --out /workspace/front.json && jq -c '.[0:2][]' /workspace/front.json"
				},
				callId: 'toolu_01t4AlGiMGHycMHxfgjOoSWX',
				name: 'bash'
			}
		},
		{
			seq: 23,
			ts: 1790628177294,
			type: 'tool.result',
			spanId: '01a0e9c1-a31a-7e18-b367-73824d20b916',
			parentSpanId: null,
			ms: 627,
			payload: {
				callId: 'toolu_01t4AlGiMGHycMHxfgjOoSWX',
				content:
					'exit code: 0\nSaved 30 stories to /workspace/front.json\n{"id":41477679,"title":"The quiet joy of cron","url":"https://example.net/cron","points":331,"comments":22}\n{"id":41858163,"title":"Reading your own flame graphs","url":"https://example.com/flame-graphs","points":320,"comments":282}\n',
				isError: false,
				meta: {
					bytes: 294,
					exitCode: 0,
					logPath: '/ump/logs/toolu_01t4AlGiMGHycMHxfgjOoSWX.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 24,
			ts: 1790628186123,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 8828,
			payload: {
				cost: 32980,
				latencyMs: 8828,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: 'Now the digest itself: sorted by points, with a ★ for self-hosting, Go and databases. "Go" only counts with a capital G, otherwise every "go" in a title matches.',
				toolCalls: [
					{
						id: 'toolu_01U0brV6epkEYyR59erbsfXV',
						name: 'write_file'
					}
				],
				turn: 7,
				usage: {
					input: 900,
					output: 2300,
					cacheRead: 23400,
					cacheWrite: 1400,
					reasoning: 0
				}
			}
		},
		{
			seq: 25,
			ts: 1790628186124,
			type: 'tool.call',
			spanId: '01a0e9c1-c80c-717c-9128-c0bf62df2b89',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					content:
						'#!/usr/bin/env python3\nimport argparse\nimport json\nimport os\nimport re\n\nparser = argparse.ArgumentParser()\nparser.add_argument("--input", required=True)\nparser.add_argument("--output", required=True)\nargs = parser.parse_args()\n\n# "Go" only counts with a capital G, the other topics match in any case\nTOPICS = re.compile(r"self-?host|homelab|postgres|sqlite|database|docker|kubernetes|\\bk8s\\b", re.IGNORECASE)\nGO = re.compile(r"\\bGo\\b|\\bGolang\\b")\n\n\ndef matches(title):\n    return bool(TOPICS.search(title) or GO.search(title))\n\n\nwith open(args.input) as f:\n    stories = json.load(f)\n\nlines = [\n    "# Hacker News digest",\n    "",\n    f"{len(stories)} front-page stories by points. ★ marks self-hosting, Go and databases.",\n    "",\n]\nmatched = 0\nfor rank, story in enumerate(stories, start=1):\n    star = ""\n    if matches(story["title"]):\n        star = "★ "\n        matched += 1\n    lines.append(f"{rank}. {star}[{story[\'title\']}]({story[\'url\']}) · {story[\'points\']} points · {story[\'comments\']} comments")\n\nos.makedirs(os.path.dirname(args.output), exist_ok=True)\nwith open(args.output, "w") as f:\n    f.write("\\n".join(lines) + "\\n")\nprint(json.dumps({"stories": len(stories), "matches": matched, "output": args.output}))\n',
					path: '/workspace/render_digest.py'
				},
				callId: 'toolu_01U0brV6epkEYyR59erbsfXV',
				name: 'write_file'
			}
		},
		{
			seq: 26,
			ts: 1790628186163,
			type: 'tool.result',
			spanId: '01a0e9c1-c80c-717c-9128-c0bf62df2b89',
			parentSpanId: null,
			ms: 39,
			payload: {
				callId: 'toolu_01U0brV6epkEYyR59erbsfXV',
				content: 'Wrote 1232 bytes to /workspace/render_digest.py.',
				isError: false,
				meta: null,
				name: 'write_file'
			}
		},
		{
			seq: 27,
			ts: 1790628189465,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 3301,
			payload: {
				cost: 16640,
				latencyMs: 3301,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: '',
				toolCalls: [
					{
						id: 'toolu_01Cr6pv7KLmqjE3724Tq1eaK',
						name: 'bash'
					}
				],
				turn: 8,
				usage: {
					input: 1500,
					output: 190,
					cacheRead: 26200,
					cacheWrite: 2600,
					reasoning: 0
				}
			}
		},
		{
			seq: 28,
			ts: 1790628189465,
			type: 'tool.call',
			spanId: '01a0e9c1-d519-7753-9a42-24ab2f0f17cb',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					command:
						'python3 /workspace/render_digest.py --input /workspace/front.json --output /ump/outputs/digest.md && head -n 7 /ump/outputs/digest.md'
				},
				callId: 'toolu_01Cr6pv7KLmqjE3724Tq1eaK',
				name: 'bash'
			}
		},
		{
			seq: 29,
			ts: 1790628189548,
			type: 'tool.result',
			spanId: '01a0e9c1-d519-7753-9a42-24ab2f0f17cb',
			parentSpanId: null,
			ms: 83,
			payload: {
				callId: 'toolu_01Cr6pv7KLmqjE3724Tq1eaK',
				content:
					'exit code: 0\n{"stories": 30, "matches": 1, "output": "/ump/outputs/digest.md"}\n# Hacker News digest\n\n30 front-page stories by points. ★ marks self-hosting, Go and databases.\n\n1. [The quiet joy of cron](https://example.net/cron) · 331 points · 22 comments\n2. [Reading your own flame graphs](https://example.com/flame-graphs) · 320 points · 282 comments\n3. [A field guide to retry budgets](https://example.org/retry-budgets) · 223 points · 64 comments\n',
				isError: false,
				meta: {
					bytes: 446,
					exitCode: 0,
					logPath: '/ump/logs/toolu_01Cr6pv7KLmqjE3724Tq1eaK.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'bash'
			}
		},
		{
			seq: 30,
			ts: 1790628192150,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 2601,
			payload: {
				cost: 13610,
				latencyMs: 2601,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: '',
				toolCalls: [
					{
						id: 'toolu_01hpbzkYYQZBops25fPQQtZb',
						name: 'remember'
					}
				],
				turn: 9,
				usage: {
					input: 1300,
					output: 150,
					cacheRead: 28800,
					cacheWrite: 1500,
					reasoning: 0
				}
			}
		},
		{
			seq: 31,
			ts: 1790628192150,
			type: 'tool.call',
			spanId: '01a0e9c1-df96-791e-9837-e2eabbeceea7',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					note: 'Ask HN threads come without a URL. The fetcher links them to their comments page.'
				},
				callId: 'toolu_01hpbzkYYQZBops25fPQQtZb',
				name: 'remember'
			}
		},
		{
			seq: 32,
			ts: 1790628192150,
			type: 'tool.result',
			spanId: '01a0e9c1-df96-791e-9837-e2eabbeceea7',
			parentSpanId: null,
			ms: 0,
			payload: {
				callId: 'toolu_01hpbzkYYQZBops25fPQQtZb',
				content: 'Noted.',
				isError: false,
				meta: null,
				name: 'remember'
			}
		},
		{
			seq: 33,
			ts: 1790628195851,
			type: 'llm.call',
			spanId: null,
			parentSpanId: null,
			ms: 3700,
			payload: {
				cost: 13610,
				latencyMs: 3700,
				model: 'claude-sonnet-5',
				reasoning: '',
				stop: 'tool_use',
				text: '',
				toolCalls: [
					{
						id: 'toolu_01NdGfqE7Nl9BaAOeOsKwOYF',
						name: 'finish'
					}
				],
				turn: 10,
				usage: {
					input: 500,
					output: 480,
					cacheRead: 30300,
					cacheWrite: 700,
					reasoning: 0
				}
			}
		},
		{
			seq: 34,
			ts: 1790628195851,
			type: 'tool.call',
			spanId: '01a0e9c1-ee0b-7a9f-b58d-e184c38f84f9',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {
					outputs: {
						matches: 1,
						stories: 30
					},
					status: 'success',
					summary:
						'Wrote the digest of 30 front-page stories to `digest.md` and marked 1 story about self-hosting, Go or databases with a ★. The top story is "The quiet joy of cron" with 331 points.'
				},
				callId: 'toolu_01NdGfqE7Nl9BaAOeOsKwOYF',
				name: 'finish'
			}
		},
		{
			seq: 35,
			ts: 1790628195852,
			type: 'tool.result',
			spanId: '01a0e9c1-ee0b-7a9f-b58d-e184c38f84f9',
			parentSpanId: null,
			ms: 0,
			payload: {
				callId: 'toolu_01NdGfqE7Nl9BaAOeOsKwOYF',
				content: 'Run finished.',
				finish: {
					status: 'success',
					summary:
						'Wrote the digest of 30 front-page stories to `digest.md` and marked 1 story about self-hosting, Go or databases with a ★. The top story is "The quiet joy of cron" with 331 points.',
					outputs: {
						matches: 1,
						stories: 30
					}
				},
				isError: false,
				meta: null,
				name: 'finish'
			}
		},
		{
			seq: 36,
			ts: 1790628195852,
			type: 'finish',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'success',
				summary:
					'Wrote the digest of 30 front-page stories to `digest.md` and marked 1 story about self-hosting, Go or databases with a ★. The top story is "The quiet joy of cron" with 331 points.',
				outputs: {
					matches: 1,
					stories: 30
				}
			}
		},
		{
			seq: 37,
			ts: 1790628195872,
			type: 'log',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				artifacts: ['digest.md'],
				message: 'Collected 1 artifact(s)'
			}
		},
		{
			seq: 38,
			ts: 1790628196152,
			type: 'sandbox.destroy',
			spanId: '01a0e9c1-ee23-7937-93dd-c761af47353c',
			parentSpanId: null,
			ms: 277,
			payload: {
				sandboxId: '39b29b796ce9441965df4cb8cb4cf4f2e12d564cefd7ae4b2d343a9c77d2141a'
			}
		},
		{
			seq: 39,
			ts: 1790628196153,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				error: '',
				status: 'succeeded'
			}
		}
	],
	artifacts: [
		{
			name: 'digest.md',
			size: 4434
		}
	],
	version: {
		version: 1,
		author: 'reflection',
		summary:
			'Kept the front-page fetcher as a toolkit script and wrote down which API to use, where Ask HN links go and how topics match.',
		sourceRunId: '01a0e9c1-1e5d-75db-9e97-7e6573a7046d',
		createdAt: 1790628206225,
		ops: [
			{
				op: 'add_learning',
				rationale: 'The run spent two turns finding the right API.',
				id: null,
				kind: 'fact',
				text: 'The Algolia search API with tags=front_page returns title, URL, points and comment count in one request. The official Firebase API needs one request per story.',
				when: 'fetching the front page',
				name: null,
				content: null,
				status: 'applied',
				target: 'L1'
			},
			{
				op: 'add_learning',
				rationale: 'The agent flagged it with remember.',
				id: null,
				kind: 'edge_case',
				text: 'Ask HN threads have no URL. Link them to their comments page on news.ycombinator.com instead of leaving the link empty.',
				when: 'building links',
				name: null,
				content: null,
				status: 'applied',
				target: 'L2'
			},
			{
				op: 'add_learning',
				rationale: 'The run chose this rule on purpose and the next run should keep it.',
				id: null,
				kind: 'preference',
				text: 'Match "Go" only with a capital G or as "Golang". Lowercase "go" appears in too many titles.',
				when: 'marking topics',
				name: null,
				content: null,
				status: 'applied',
				target: 'L3'
			},
			{
				op: 'upsert_script',
				rationale: 'The fetcher worked on the first try and every run needs it.',
				id: null,
				kind: null,
				text: null,
				when: null,
				name: 'fetch_front_page',
				content:
					'#!/usr/bin/env python3\n# ump:name        fetch_front_page\n# ump:description Fetch the Hacker News front page and save the stories as JSON, sorted by points\n# ump:args        {"limit":"integer","out":"string"}\n# ump:side-effects none\nimport argparse\nimport json\nimport urllib.request\n\nparser = argparse.ArgumentParser()\nparser.add_argument("--limit", type=int, default=30)\nparser.add_argument("--out", required=True)\nargs = parser.parse_args()\n\n# The Algolia API returns points and comment counts in one request, the Firebase API needs one request per story\nurl = f"https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage={args.limit}"\nwith urllib.request.urlopen(url, timeout=20) as response:\n    hits = json.load(response)["hits"]\n\n# Ask HN and Show HN posts have no URL, so they link to their comments page\nstories = [\n    {\n        "id": int(hit["objectID"]),\n        "title": hit["title"],\n        "url": hit.get("url") or f"https://news.ycombinator.com/item?id={hit[\'objectID\']}",\n        "points": hit.get("points") or 0,\n        "comments": hit.get("num_comments") or 0,\n    }\n    for hit in hits\n]\nstories.sort(key=lambda story: story["points"], reverse=True)\n\nwith open(args.out, "w") as f:\n    json.dump(stories, f, indent=2)\nprint(f"Saved {len(stories)} stories to {args.out}")\n',
				status: 'applied',
				target: 'fetch_front_page'
			},
			{
				op: 'set_dockerfile',
				rationale: 'A slim Python image would start faster than the default sandbox image.',
				id: null,
				kind: null,
				text: null,
				when: null,
				name: null,
				content:
					'FROM python:3.13-slim\nRUN apt-get update && apt-get install -y --no-install-recommends curl jq && rm -rf /var/lib/apt/lists/*',
				status: 'held',
				target: 'dockerfile',
				flags: ['builds on a new base image python:3.13-slim']
			}
		],
		content: {
			learnings: [
				{
					id: 'L1',
					kind: 'fact',
					text: 'The Algolia search API with tags=front_page returns title, URL, points and comment count in one request. The official Firebase API needs one request per story.',
					when: 'fetching the front page',
					sources: ['01a0e9c1-1e5d-75db-9e97-7e6573a7046d'],
					hits: 0,
					status: 'active'
				},
				{
					id: 'L2',
					kind: 'edge_case',
					text: 'Ask HN threads have no URL. Link them to their comments page on news.ycombinator.com instead of leaving the link empty.',
					when: 'building links',
					sources: ['01a0e9c1-1e5d-75db-9e97-7e6573a7046d'],
					hits: 0,
					status: 'active'
				},
				{
					id: 'L3',
					kind: 'preference',
					text: 'Match "Go" only with a capital G or as "Golang". Lowercase "go" appears in too many titles.',
					when: 'marking topics',
					sources: ['01a0e9c1-1e5d-75db-9e97-7e6573a7046d'],
					hits: 0,
					status: 'active'
				}
			],
			toolkit: [
				{
					name: 'fetch_front_page',
					lang: 'python',
					description:
						'Fetch the Hacker News front page and save the stories as JSON, sorted by points',
					args: {
						limit: 'integer',
						out: 'string'
					},
					sideEffects: false,
					content:
						'#!/usr/bin/env python3\n# ump:name        fetch_front_page\n# ump:description Fetch the Hacker News front page and save the stories as JSON, sorted by points\n# ump:args        {"limit":"integer","out":"string"}\n# ump:side-effects none\nimport argparse\nimport json\nimport urllib.request\n\nparser = argparse.ArgumentParser()\nparser.add_argument("--limit", type=int, default=30)\nparser.add_argument("--out", required=True)\nargs = parser.parse_args()\n\n# The Algolia API returns points and comment counts in one request, the Firebase API needs one request per story\nurl = f"https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage={args.limit}"\nwith urllib.request.urlopen(url, timeout=20) as response:\n    hits = json.load(response)["hits"]\n\n# Ask HN and Show HN posts have no URL, so they link to their comments page\nstories = [\n    {\n        "id": int(hit["objectID"]),\n        "title": hit["title"],\n        "url": hit.get("url") or f"https://news.ycombinator.com/item?id={hit[\'objectID\']}",\n        "points": hit.get("points") or 0,\n        "comments": hit.get("num_comments") or 0,\n    }\n    for hit in hits\n]\nstories.sort(key=lambda story: story["points"], reverse=True)\n\nwith open(args.out, "w") as f:\n    json.dump(stories, f, indent=2)\nprint(f"Saved {len(stories)} stories to {args.out}")\n',
					sources: ['01a0e9c1-1e5d-75db-9e97-7e6573a7046d'],
					stats: {
						calls: 0,
						failures: 0
					}
				}
			],
			dockerfile: null,
			setup: null,
			main: null
		},
		previous: {
			learnings: [],
			toolkit: [],
			dockerfile: null,
			setup: null,
			main: null
		}
	}
};

// After graduating the job runs its main script without the model
export const scriptedRun: RecordedRun = {
	run: {
		id: '01a0e9c3-8178-72f9-8117-4a1713fd0960',
		jobId: '01a0e9c1-0719-72c0-abb4-03f2b1fc1ffb',
		jobName: 'Hacker News digest',
		number: 6,
		status: 'succeeded',
		mode: 'scripted',
		trigger: 'schedule',
		queuedAt: 1790628299128,
		startedAt: 1790628299130,
		finishedAt: 1790628300448,
		msQueue: 2,
		msProvision: 298,
		msLlm: 0,
		msTools: 647,
		msTotal: 1317,
		turns: 0,
		tokIn: 0,
		tokOut: 0,
		tokCacheRead: 0,
		tokCacheWrite: 0,
		cost: 0,
		modelName: 'claude-sonnet-5',
		modelLabel: 'Claude Sonnet 5',
		summary:
			'Wrote the digest of 30 front-page stories to digest.md and marked 1 on our topics with a ★.',
		error: null,
		sandboxIsolation: 'container',
		input: null,
		instructions: null,
		outputs: {
			matches: 1,
			stories: 30
		},
		playbookVersion: 3,
		imageRef: 'ghcr.io/stonith404/umpteenth-sandbox:latest',
		sandboxAdapter: 'docker',
		triggeredBy: null,
		triggeredByName: null,
		jobDeleted: false,
		fellBack: false,
		cancelRequested: false,
		reflection: 'skipped',
		reflectionError: null,
		reflectionSummary: null,
		reflectionOps: [],
		reflectionVersion: null,
		reflectionCost: 0,
		reflectionTokens: 0,
		verifyCost: 0,
		verifyTokens: 0
	},
	events: [
		{
			seq: 1,
			ts: 1790628299130,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'provisioning'
			}
		},
		{
			seq: 2,
			ts: 1790628299428,
			type: 'sandbox.create',
			spanId: '01a0e9c3-817b-79f7-ae53-17651924f470',
			parentSpanId: null,
			ms: 296,
			payload: {
				adapter: 'docker',
				image: 'ghcr.io/stonith404/umpteenth-sandbox:latest',
				isolation: 'container',
				sandboxId: 'fabb9117a7335ca3db0687888d88eca4418617b49e86bc4d220a1ecbd9c0a112'
			}
		},
		{
			seq: 3,
			ts: 1790628299455,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'running'
			}
		},
		{
			seq: 4,
			ts: 1790628299455,
			type: 'tool.call',
			spanId: '01a0e9c3-82bf-7ba9-bc1d-663fbd5ac905',
			parentSpanId: null,
			ms: null,
			payload: {
				args: {},
				callId: 'main',
				name: 'main'
			}
		},
		{
			seq: 5,
			ts: 1790628299525,
			type: 'script.step',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				name: 'fetch front page'
			}
		},
		{
			seq: 6,
			ts: 1790628299525,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: 0,
			payload: {
				endpoint: 'POST /v1/step',
				ok: true
			}
		},
		{
			seq: 7,
			ts: 1790628299613,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				endpoint: 'proxy hn.algolia.com',
				ok: true
			}
		},
		{
			seq: 8,
			ts: 1790628300047,
			type: 'script.step',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				name: 'render digest'
			}
		},
		{
			seq: 9,
			ts: 1790628300047,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: 0,
			payload: {
				endpoint: 'POST /v1/step',
				ok: true
			}
		},
		{
			seq: 10,
			ts: 1790628300076,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: 0,
			payload: {
				endpoint: 'POST /v1/output',
				ok: true
			}
		},
		{
			seq: 11,
			ts: 1790628300082,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: 0,
			payload: {
				endpoint: 'POST /v1/output',
				ok: true
			}
		},
		{
			seq: 12,
			ts: 1790628300086,
			type: 'broker.call',
			spanId: null,
			parentSpanId: null,
			ms: 0,
			payload: {
				endpoint: 'POST /v1/summary',
				ok: true
			}
		},
		{
			seq: 13,
			ts: 1790628300102,
			type: 'tool.result',
			spanId: '01a0e9c3-82bf-7ba9-bc1d-663fbd5ac905',
			parentSpanId: null,
			ms: 647,
			payload: {
				callId: 'main',
				content: 'exit code: 0\nSaved 30 stories to /workspace/front.json\n',
				isError: false,
				meta: {
					bytes: 42,
					exitCode: 0,
					logPath: '/ump/logs/main.txt',
					oomKilled: false,
					timedOut: false
				},
				name: 'main'
			}
		},
		{
			seq: 14,
			ts: 1790628300103,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				status: 'verifying'
			}
		},
		{
			seq: 15,
			ts: 1790628300127,
			type: 'verify.result',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				checks: [
					{
						check: 'exit_code == 0',
						passed: true
					},
					{
						check: 'output.stories exists',
						passed: true
					},
					{
						check: 'output.matches exists',
						passed: true
					},
					{
						check: 'output.stories == 30',
						passed: true
					},
					{
						check: 'file /ump/outputs/digest.md exists',
						passed: true
					}
				],
				passed: true
			}
		},
		{
			seq: 16,
			ts: 1790628300133,
			type: 'log',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				artifacts: ['digest.md'],
				message: 'Collected 1 artifact(s)'
			}
		},
		{
			seq: 17,
			ts: 1790628300447,
			type: 'sandbox.destroy',
			spanId: '01a0e9c3-8567-7d89-bbe2-cafe1b700184',
			parentSpanId: null,
			ms: 311,
			payload: {
				sandboxId: 'fabb9117a7335ca3db0687888d88eca4418617b49e86bc4d220a1ecbd9c0a112'
			}
		},
		{
			seq: 18,
			ts: 1790628300448,
			type: 'run.status',
			spanId: null,
			parentSpanId: null,
			ms: null,
			payload: {
				error: '',
				status: 'succeeded'
			}
		}
	],
	artifacts: [
		{
			name: 'digest.md',
			size: 4504
		}
	],
	version: null
};
