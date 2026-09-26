// Self-improvement eval (PLAN.md §17): runs reference jobs several times in a row with learning on, and reports how their turns, cost and duration develop
//
// Environment:
//   APP_URL        Umpteenth to evaluate, default http://localhost:8080
//   API_TOKEN      an ump_ API token; without one, the e2e test session endpoint is used
//   EVAL_RUNS      runs per job, default 7, which leaves room for a job to graduate after its fourth run
//   EVAL_JOBS      comma-separated job keys to run, default all
//   EVAL_BASE_URL, EVAL_API_KEY, EVAL_MODEL  an OpenAI-compatible endpoint and model to register and use, otherwise the workspace default model runs the jobs
import fs from 'node:fs';
import path from 'node:path';

const BASE = (process.env.APP_URL ?? 'http://localhost:8080').replace(/\/$/, '');
const RUNS = Number(process.env.EVAL_RUNS ?? 7);
const ONLY = process.env.EVAL_JOBS ? process.env.EVAL_JOBS.split(',') : null;
const RUN_TIMEOUT_MS = 20 * 60_000;
const REFLECTION_TIMEOUT_MS = 10 * 60_000;

const messyCsv = `name, email ,signup
  Ada Lovelace , ADA@Example.com, 2024-01-05
Grace Hopper,grace@navy.mil ,05/02/2024
"Hopper, Grace",GRACE@navy.mil,2024-02-05
Alan Turing,,Mar 3 2024
Linus Torvalds,linus@kernel.org,Dec 31 2023
 Margaret Hamilton ,margaret@mit.edu,2023-07-20
`;

// The reference jobs: API calls with a judgment step, pure shell, data wrangling, web fetch with job state, and a job that needs a package the image lacks
const JOBS = [
	{
		key: 'hn-digest',
		network: 'internet',
		instruction:
			'Report the current top 5 stories on Hacker News using the official API at https://hacker-news.firebaseio.com/v0/. For each story give the title, score and link as a markdown list in the summary. Output top_story_id (integer) and top_score (integer).'
	},
	{
		key: 'tls-expiry',
		network: 'internet',
		instruction:
			'Check the TLS certificates of example.com, github.com, wikipedia.org and cloudflare.com on port 443. Output days_left as an object mapping each domain to the whole days until its certificate expires, and soonest as the domain whose certificate expires first. The run fails if any certificate expires within 14 days.'
	},
	{
		key: 'csv-clean',
		network: 'none',
		input: { csv: messyCsv },
		instruction:
			"Clean the CSV in the run input's csv field. Trim whitespace in every field, lowercase the email addresses, convert the signup dates to ISO 8601 (YYYY-MM-DD; they come as YYYY-MM-DD, DD/MM/YYYY or like 'Jan 5 2024'), drop rows without an email, and keep only the first row for each email. Write the result as a JSON array of objects with the keys name, email and signup_date to /ump/outputs/clean.json, and output row_count."
	},
	{
		key: 'page-watch',
		network: 'internet',
		instruction:
			"Check whether the page https://www.iana.org/help/example-domains changed since the last run. Hash the page's visible text, compare the hash with the one this job stored in its state last time, and store the new hash. Output changed (boolean, true on the first run) and text_length (integer)."
	},
	{
		// A webhook-style job whose input format changes: from run 8 on the orders carry "total" instead of "amount", which breaks a graduated main script
		key: 'orders',
		network: 'none',
		runs: 10,
		input: (run) => ({ orders: orders(run >= 8 ? 'total' : 'amount') }),
		breaksAtRun: 8,
		instruction:
			'Sum the order amounts in the run input per currency. Output totals (an object mapping each currency to the sum of its orders, rounded to 2 decimals) and order_count (integer).'
	},
	{
		key: 'co2-chart',
		network: 'internet',
		instruction:
			'Download the monthly Mauna Loa CO2 data from https://raw.githubusercontent.com/datasets/co2-ppm/main/data/co2-mm-mlo.csv and plot the monthly average CO2 of the last 10 years as a line chart with matplotlib. Save it as /ump/outputs/co2.png and output latest_ppm (number) and latest_month (string, YYYY-MM).'
	}
];

// The orders of the orders job, with the amount under the given field name
function orders(field) {
	return [
		{ id: 1, [field]: 19.99, currency: 'EUR' },
		{ id: 2, [field]: 5.01, currency: 'EUR' },
		{ id: 3, [field]: 100, currency: 'USD' },
		{ id: 4, [field]: 0.5, currency: 'CHF' }
	];
}

let cookie = '';

async function api(method, url, body) {
	const headers = { 'Content-Type': 'application/json' };
	if (process.env.API_TOKEN) headers.Authorization = `Bearer ${process.env.API_TOKEN}`;
	if (cookie) headers.Cookie = cookie;
	const res = await fetch(BASE + url, {
		method,
		headers,
		body: body ? JSON.stringify(body) : undefined
	});
	const text = await res.text();
	if (!res.ok) throw new Error(`${method} ${url} -> ${res.status}: ${text.slice(0, 500)}`);
	return text ? JSON.parse(text) : null;
}

async function authenticate() {
	if (process.env.API_TOKEN) return;
	const res = await fetch(BASE + '/api/test/session', { method: 'POST' });
	if (!res.ok) throw new Error('Set API_TOKEN, the test session endpoint is not available');
	cookie = res.headers
		.getSetCookie()
		.map((c) => c.split(';')[0])
		.join('; ');
}

// Registers the evaluated model once, so the jobs, reflection and ump llm all use it
async function setupModel() {
	const { EVAL_BASE_URL, EVAL_API_KEY, EVAL_MODEL } = process.env;
	if (!EVAL_BASE_URL || !EVAL_API_KEY || !EVAL_MODEL) return undefined;
	const providers = await api('GET', '/api/providers?pageSize=100');
	let provider = providers.items.find((p) => p.name === 'Eval gateway');
	if (!provider) {
		provider = await api('POST', '/api/providers', {
			name: 'Eval gateway',
			kind: 'openai',
			baseUrl: EVAL_BASE_URL,
			apiKey: EVAL_API_KEY
		});
	}
	const models = await api('GET', '/api/models?pageSize=100');
	let model = models.items.find((m) => m.providerId === provider.id && m.model === EVAL_MODEL);
	if (!model) {
		model = await api('POST', '/api/models', {
			providerId: provider.id,
			model: EVAL_MODEL,
			label: `${EVAL_MODEL} (eval)`,
			// Nominal prices make the cost curve visible, the gateway's real prices are unknown here
			price: { in: 1_250_000, out: 10_000_000, cacheRead: 125_000, cacheWrite: 0 },
			caps: {
				tools: true,
				parallelTools: true,
				reasoning: true,
				jsonSchema: true,
				promptCache: true,
				vision: false,
				context: 200_000
			}
		});
	}
	await api('PATCH', '/api/settings', { agentModelId: model.id, utilityModelId: model.id });
	return model.id;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(check, timeoutMs, what) {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const value = await check();
		if (value) return value;
		if (Date.now() > deadline) throw new Error(`Timed out waiting for ${what}`);
		await sleep(2000);
	}
}

const TERMINAL = ['succeeded', 'failed', 'cancelled', 'timed_out', 'skipped'];

// Runs one job RUNS times, waiting for each run's reflection so the next run starts from what it learned
async function evaluate(def, modelId, stamp) {
	const job = await api('POST', '/api/jobs', {
		name: `Eval ${def.key} ${stamp}`,
		instruction: def.instruction,
		network: def.network,
		selfImprove: true,
		concurrency: 'skip',
		modelId,
		limits: { maxTurns: 40, timeoutSeconds: 900, maxCostUsd: 5 }
	});
	const results = [];
	const runs = def.runs ?? RUNS;
	for (let i = 1; i <= runs; i++) {
		const input = typeof def.input === 'function' ? def.input(i) : def.input;
		const { runId } = await api('POST', `/api/jobs/${job.id}/runs`, input ? { input } : {});
		let run = await waitFor(
			async () => {
				const r = await api('GET', `/api/runs/${runId}`);
				return TERMINAL.includes(r.status) ? r : null;
			},
			RUN_TIMEOUT_MS,
			`run ${i} of ${def.key}`
		);
		run = await waitFor(
			async () => {
				const r = await api('GET', `/api/runs/${runId}`);
				return r.reflection === 'pending' ? null : r;
			},
			REFLECTION_TIMEOUT_MS,
			`reflection on run ${i} of ${def.key}`
		);
		const row = {
			run: i,
			id: run.id,
			status: run.status,
			mode: run.mode,
			playbookVersion: run.playbookVersion,
			fellBack: run.fellBack,
			turns: run.turns,
			cost: run.cost / 1e6,
			verifyCost: run.verifyCost / 1e6,
			seconds: Math.round((run.msTotal ?? 0) / 100) / 10,
			reflection: run.reflection,
			learnedVersion: run.reflectionVersion,
			reflectionCost: run.reflectionCost / 1e6,
			ops: (run.reflectionOps ?? []).map((o) => `${o.op}:${o.status}`),
			error: run.error ?? run.reflectionError ?? null
		};
		results.push(row);
		console.log(
			`[${def.key}] run ${i}: ${row.status}, ${row.mode}${row.fellBack ? ' (fell back)' : ''}, ${row.turns} turns, $${row.cost.toFixed(4)}, ${row.seconds}s, reflection ${row.reflection}${row.learnedVersion ? ` -> v${row.learnedVersion}` : ''} ${row.ops.join(' ')}${row.error ? ` (${row.error.slice(0, 120)})` : ''}`
		);
	}

	// A Dockerfile written by reflection that a later run succeeded with means the job moved a repeated install into its environment
	const playbook = await api('GET', `/api/jobs/${job.id}/playbook`);
	const versions = await api(
		'GET',
		`/api/jobs/${job.id}/playbook/versions?pageSize=100&sort=version`
	);
	let dockerfileByReflection = false;
	for (const v of versions.items.filter((v) => v.author === 'reflection')) {
		const detail = await api('GET', `/api/jobs/${job.id}/playbook/versions/${v.version}`);
		const changed =
			detail.content.dockerfile && detail.content.dockerfile !== detail.previous?.dockerfile;
		if (changed && results.some((r) => r.status === 'succeeded' && r.playbookVersion >= v.version))
			dockerfileByReflection = true;
	}
	return {
		key: def.key,
		breaksAtRun: def.breaksAtRun,
		jobId: job.id,
		runs: results,
		playbook: {
			version: playbook.version,
			learnings: playbook.content.learnings.filter((l) => l.status !== 'retired').length,
			scripts: playbook.content.toolkit.map((s) => s.name),
			dockerfile: playbook.content.dockerfile
		},
		dockerfileByReflection
	};
}

function summarize(reports) {
	console.log(
		'\njob          turns per run                          success  run1→3 turns  Dockerfile'
	);
	let improved = 0;
	for (const r of reports) {
		const turns = r.runs.map((x) => x.turns);
		const ok = r.runs.filter((x) => x.status === 'succeeded').length;
		// Only successful runs count, since a run that fails early also takes few turns
		const comparable =
			r.runs.length >= 3 && r.runs[0].status === 'succeeded' && r.runs[2].status === 'succeeded';
		const drop = comparable && turns[0] > 0 ? (turns[0] - turns[2]) / turns[0] : 0;
		if (drop >= 0.5) improved++;
		r.turnDrop = drop;
		console.log(
			`${r.key.padEnd(12)} ${turns.join(' ').padEnd(38)} ${`${ok}/${r.runs.length}`.padEnd(8)} ${`${Math.round(drop * 100)}%`.padEnd(13)} ${r.dockerfileByReflection ? 'learned' : '-'}`
		);
	}
	const withDockerfile = reports.filter((r) => r.dockerfileByReflection).length;
	console.log(
		`\nTurns dropped by at least 50% from run 1 to run 3 for ${improved} of ${reports.length} jobs (M4 needs 3 of 5)`
	);
	console.log(`${withDockerfile} job(s) moved an install into their Dockerfile (M4 needs 1)`);

	// Graduation: scripted runs that passed on their own, and what they cost compared to the first run
	let cheap = 0;
	console.log('\njob          first scripted  scripted ok  scripted cost vs run 1');
	for (const r of reports) {
		const scripted = r.runs.filter(
			(x) => x.mode === 'scripted' && x.status === 'succeeded' && !x.fellBack
		);
		const first = r.runs.find((x) => x.mode === 'scripted');
		const run1 = r.runs[0].cost;
		const avg = scripted.length
			? scripted.reduce((sum, x) => sum + x.cost + x.verifyCost, 0) / scripted.length
			: null;
		const ratio = avg !== null && run1 > 0 ? avg / run1 : null;
		if (ratio !== null && ratio <= 0.1) cheap++;
		r.scriptedCostRatio = ratio;
		console.log(
			`${r.key.padEnd(12)} ${(first ? `run ${first.run}` : '-').padEnd(15)} ${String(scripted.length).padEnd(12)} ${ratio === null ? '-' : `${(ratio * 100).toFixed(1)}%`}`
		);
	}
	console.log(
		`\n${cheap} job(s) run scripted at no more than 10% of their first run's cost (M5 needs 2)`
	);

	// Breakage: the run whose input changed falls back and finishes, reflection repairs main, and the next run passes on its own
	let repaired = false;
	for (const r of reports.filter((x) => x.breaksAtRun)) {
		const broken = r.runs[r.breaksAtRun - 1];
		const next = r.runs[r.breaksAtRun];
		// A repair may fix main itself or the toolkit script main calls, whichever broke
		const fixed = Boolean(
			broken?.ops.some((o) => o === 'update_main:applied' || o === 'upsert_script:applied')
		);
		repaired = Boolean(
			broken?.mode === 'scripted' &&
			broken.fellBack &&
			broken.status === 'succeeded' &&
			fixed &&
			next?.mode === 'scripted' &&
			next.status === 'succeeded' &&
			!next.fellBack
		);
		console.log(
			`${r.key}: run ${r.breaksAtRun} was ${broken?.mode}${broken?.fellBack ? ', fell back and ' + broken.status : ''}; ${fixed ? 'repaired' : 'not repaired'} by reflection; run ${r.breaksAtRun + 1} was ${next?.mode} and ${next?.status}${next?.fellBack ? ' after another fallback' : ''} (M5 needs a fallback, a repair and a passing next run)`
		);
	}
	return { improved, withDockerfile, cheap, repaired };
}

await authenticate();
const modelId = await setupModel();
const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, '-');
const jobs = JOBS.filter((j) => !ONLY || ONLY.includes(j.key));
console.log(`Evaluating ${jobs.length} job(s) with ${RUNS} runs each against ${BASE}`);

// Jobs run side by side, each job's runs strictly one after another
const settled = await Promise.allSettled(jobs.map((def) => evaluate(def, modelId, stamp)));
const reports = [];
for (const [i, s] of settled.entries()) {
	if (s.status === 'fulfilled') reports.push(s.value);
	else console.error(`[${jobs[i].key}] failed: ${s.reason?.message ?? s.reason}`);
}
const verdict = summarize(reports);

const outDir = path.join(import.meta.dirname, '..', '.tmp', 'eval');
fs.mkdirSync(outDir, { recursive: true });
const outFile = path.join(outDir, `eval-${stamp}.json`);
fs.writeFileSync(outFile, JSON.stringify({ base: BASE, runs: RUNS, verdict, reports }, null, 2));
console.log(`Results written to ${outFile}`);
