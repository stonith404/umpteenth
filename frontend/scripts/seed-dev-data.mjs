// Seeds an e2etest backend with jobs and runs of mixed outcomes, so tables, charts and the dashboard have data during development
// Usage: node scripts/seed-dev-data.mjs [backendUrl] [sqliteDbPath]
// It wipes the backend first, so never point it at a real instance
// With a SQLite database path, the runs are spread over the last three weeks afterwards, since the API can only create runs "now"

import { execFileSync } from 'node:child_process';

const BASE = process.argv[2] ?? 'http://localhost:18200';
const DB_PATH = process.argv[3];

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled', 'timed_out', 'skipped']);
let cookie = '';

async function api(method, path, body) {
	const res = await fetch(BASE + path, {
		method,
		headers: { 'Content-Type': 'application/json', Cookie: cookie },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
	const setCookie = res.headers.get('set-cookie');
	if (setCookie) cookie = setCookie.split(';')[0];
	const text = await res.text();
	return text ? JSON.parse(text) : null;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Queues the fake model's answers for the next run, starts it and waits until it is final
async function runJob(jobId, responses, { cancelAfterMs } = {}) {
	await api('POST', '/api/test/llm-script', { reset: true, responses });
	const { runId } = await api('POST', `/api/jobs/${jobId}/runs`, {});

	// Cancelling waits until the run executes, so the cancel reaches a live sandbox
	if (cancelAfterMs) {
		for (let i = 0; i < 100; i++) {
			const run = await api('GET', `/api/runs/${runId}`);
			if (run.status === 'running') break;
			await sleep(200);
		}
		await sleep(cancelAfterMs);
		await api('POST', `/api/runs/${runId}/cancel`);
	}

	for (let i = 0; i < 300; i++) {
		const run = await api('GET', `/api/runs/${runId}`);
		if (TERMINAL.has(run.status)) return run;
		await sleep(250);
	}
	throw new Error(`Run ${runId} did not finish`);
}

const usage = (scale) => ({
	input: Math.round(1800 * scale + Math.random() * 400),
	output: Math.round(420 * scale + Math.random() * 120),
	cacheRead: Math.round(900 * scale)
});

function successScript(scale, n) {
	return [
		{
			text: 'Let me look at what is in the workspace first.',
			reasoning: 'The job wants a summary, so I list the files and count them.',
			toolCalls: [
				{
					name: 'bash',
					args: {
						command: `mkdir -p /ump/outputs && ls -la /ump && echo '{"items":${n}}' > /ump/outputs/report.json`
					}
				}
			],
			usage: usage(scale),
			delayMs: 300
		},
		{
			text: 'Everything looks good, wrapping up.',
			toolCalls: [
				{
					name: 'finish',
					args: {
						status: 'success',
						summary: `## Done\n- Checked **${n}** items\n- Wrote \`report.json\``,
						outputs: { count: n, healthy: true }
					}
				}
			],
			usage: usage(scale * 0.6)
		}
	];
}

function failureScript(scale) {
	return [
		{
			text: 'Checking the endpoint.',
			toolCalls: [
				{
					name: 'bash',
					args: { command: 'echo "connecting…"; echo "error: connection refused" >&2; exit 7' }
				}
			],
			usage: usage(scale)
		},
		{
			toolCalls: [
				{
					name: 'finish',
					args: { status: 'failure', summary: 'The endpoint refused the connection (exit code 7).' }
				}
			],
			usage: usage(scale * 0.4)
		}
	];
}

const slowScript = [
	{
		text: 'Running the long check.',
		toolCalls: [
			{ name: 'bash', args: { command: 'for i in $(seq 1 60); do echo "tick $i"; sleep 1; done' } }
		],
		usage: usage(1)
	},
	{
		toolCalls: [
			{ name: 'finish', args: { status: 'success', summary: 'Finished the long check.' } }
		]
	}
];

async function main() {
	await api('POST', '/api/test/reset');
	await api('POST', '/api/test/session');

	// The fake model is free by default, prices make the cost charts meaningful
	const models = await api('GET', '/api/models');
	for (const model of models.items) {
		await api('PATCH', `/api/models/${model.id}`, {
			caps: model.caps,
			label: model.label ?? undefined,
			price: { in: 3_000_000, out: 15_000_000, cacheRead: 300_000, cacheWrite: 3_750_000 }
		});
	}

	const jobs = {};
	for (const [key, name, instruction] of [
		[
			'digest',
			'Stale PR digest',
			'List open pull requests without activity for 7 days and post a digest.'
		],
		[
			'backup',
			'Nightly backup check',
			'Verify that last night’s backups exist and are restorable.'
		],
		['deps', 'Dependency audit', 'Audit the dependencies for known vulnerabilities.'],
		['uptime', 'Uptime report', 'Summarize the uptime of all monitored endpoints.'],
		['invoices', 'Invoice reminders', 'Send reminders for overdue invoices.']
	]) {
		const job = await api('POST', '/api/jobs', { name, instruction });
		jobs[key] = job.id;
	}

	// Each entry is one run: the job, the scripted outcome and the relative token scale
	const plan = [];
	for (let i = 0; i < 8; i++) plan.push(['digest', 'success', 3 - i * 0.3]);
	for (let i = 0; i < 6; i++) plan.push(['backup', i === 2 ? 'failure' : 'success', 1]);
	for (let i = 0; i < 5; i++) plan.push(['deps', i % 2 === 0 ? 'failure' : 'success', 1.5]);
	for (let i = 0; i < 6; i++) plan.push(['uptime', 'success', 2 - i * 0.25]);
	for (let i = 0; i < 3; i++) plan.push(['invoices', 'success', 0.8]);
	plan.push(['invoices', 'cancel', 1]);
	plan.push(['uptime', 'cancel', 1]);

	const runIds = [];
	for (const [index, [key, outcome, scale]] of plan.entries()) {
		let run;
		if (outcome === 'success') run = await runJob(jobs[key], successScript(scale, 10 + index));
		else if (outcome === 'failure') run = await runJob(jobs[key], failureScript(scale));
		else run = await runJob(jobs[key], slowScript, { cancelAfterMs: 1500 });
		runIds.push(run.id);
		console.log(`#${index + 1} ${key}: ${run.status}`);
	}

	// A second trigger while the first run is live is skipped by the default concurrency policy
	await api('POST', '/api/test/llm-script', { reset: true, responses: slowScript });
	const first = await api('POST', `/api/jobs/${jobs.deps}/runs`, {});
	const second = await api('POST', `/api/jobs/${jobs.deps}/runs`, {});
	console.log(`skip check: ${second.status}`);
	await sleep(1500);
	await api('POST', `/api/runs/${first.runId}/cancel`).catch(() => {});

	if (DB_PATH) backdate(runIds);
}

// Spreads the seeded runs over the last three weeks, oldest first, so per-day charts and "getting cheaper" have history
function backdate(runIds) {
	const dayMs = 24 * 60 * 60 * 1000;
	const statements = runIds.map((id, i) => {
		const shift = Math.round(
			((runIds.length - i) / runIds.length) * 21 * dayMs + Math.random() * 6 * 60 * 60 * 1000
		);
		return `UPDATE runs SET queued_at = queued_at - ${shift}, started_at = started_at - ${shift}, finished_at = finished_at - ${shift} WHERE id = '${id}';
UPDATE run_events SET ts = ts - ${shift} WHERE run_id = '${id}';`;
	});
	execFileSync('sqlite3', ['-cmd', '.timeout 5000', DB_PATH], { input: statements.join('\n') });
	console.log(`backdated ${runIds.length} runs`);
}

await main();
