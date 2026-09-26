import { expect, test, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil, { type ScriptedResponse } from '../utils/run.util';

// Runs use real sandboxes and some tests build a job image, so these specs get more time than the suite default
test.describe.configure({ timeout: 120_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend();
	await authUtil.authenticate(page);
});

const SANDBOX_IMAGE = 'ghcr.io/stonith404/umpteenth-sandbox:latest';

type Op = {
	op: string;
	rationale?: string;
	id?: string;
	kind?: string;
	text?: string;
	name?: string;
	content?: string | null;
};

// One structured reflection answer, which the fake model returns as native JSON output
function reflection(summary: string, ops: Op[], usedLearnings: string[] = []): ScriptedResponse {
	const full = ops.map((op) => ({
		rationale: 'It will save turns next time.',
		id: null,
		kind: null,
		text: null,
		when: null,
		name: null,
		content: null,
		...op
	}));
	return { text: JSON.stringify({ summary, usedLearnings, ops: full }) };
}

function finish(summary: string): ScriptedResponse {
	return { toolCalls: [{ name: 'finish', args: { status: 'success', summary } }] };
}

async function createLearningJob(request: APIRequestContext, name: string, selfImprove = true) {
	const response = await request.post('/api/jobs', {
		data: { name, instruction: 'Report the top stories.', selfImprove }
	});
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { id: string };
}

// Waits until reflection on a run ended and returns the run
async function waitForReflection(request: APIRequestContext, runId: string) {
	let run: { reflection: string; reflectionVersion: number | null } = {
		reflection: '',
		reflectionVersion: null
	};
	await expect
		.poll(
			async () => {
				run = await (await request.get(`/api/runs/${runId}`)).json();
				return run.reflection;
			},
			{ timeout: 30_000, intervals: [250] }
		)
		.toMatch(/done|failed/);
	return run;
}

// The tool results of a run, keyed by tool name
async function toolResults(request: APIRequestContext, runId: string) {
	const events = (await (await request.get(`/api/runs/${runId}/events?limit=500`)).json()) as {
		type: string;
		payload: { name?: string; content?: string };
	}[];
	return events
		.filter((e) => e.type === 'tool.result')
		.map((e) => ({ name: e.payload.name ?? '', content: e.payload.content ?? '' }));
}

const topStories = `#!/usr/bin/env python3
# ump:name        top_stories
# ump:description Print the top stories as JSON
# ump:args        {"count":"integer","label":"string?"}
# ump:side-effects none
import argparse, json
parser = argparse.ArgumentParser()
parser.add_argument("--count", type=int, required=True)
parser.add_argument("--label", default="none")
args = parser.parse_args()
print(json.dumps({"count": args.count, "label": args.label}))`;

test('Reflection turns a run into a playbook version whose script the next run calls as a tool', async ({
	page
}) => {
	const job = await createLearningJob(page.request, 'Learning job');

	// The first run explores, and reflection promotes its script, adds a learning and holds back an unknown base image
	const first = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'bash', args: { command: 'echo exploring' } }] },
		finish('Explored the stories'),
		reflection('Promoted the story script', [
			{ op: 'add_learning', kind: 'edge_case', text: 'Ask HN posts have no URL' },
			{ op: 'upsert_script', content: topStories },
			{ op: 'set_dockerfile', content: 'FROM docker.io/somebody/image\nRUN true' }
		])
	]);
	expect(first.status).toBe('succeeded');
	const learned = await waitForReflection(page.request, first.runId);
	expect(learned.reflection).toBe('done');
	expect(learned.reflectionVersion).toBe(1);

	// The run's Learned tab explains every operation and shows the diff
	await page.goto(`/runs/${first.runId}?tab=learned`);
	await expect(page.getByText('What this run taught the job')).toBeVisible();
	await expect(page.getByText('Promoted the story script')).toBeVisible();
	await expect(page.getByText('Add learning L1')).toBeVisible();
	await expect(page.getByText('Save script top_stories')).toBeVisible();
	await expect(page.getByText('Held for review')).toBeVisible();
	await expect(page.getByRole('link', { name: 'playbook version 1' })).toBeVisible();
	await expect(
		page.getByText('Added: [L1] (edge_case, active, hits 0) Ask HN posts have no URL')
	).toBeAttached();

	// The next run is assisted and calls the script as a tool, with its arguments as flags
	const second = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'toolkit__top_stories', args: { count: 2, label: 'a b' } }] },
		finish('Reported the stories'),
		reflection('Nothing new', [], ['L1'])
	]);
	expect(second.status).toBe('succeeded');
	const run = (await (await page.request.get(`/api/runs/${second.runId}`)).json()) as {
		mode: string;
	};
	expect(run.mode).toBe('assisted');
	const results = await toolResults(page.request, second.runId);
	const toolkit = results.find((r) => r.name === 'toolkit__top_stories');
	expect(toolkit?.content).toContain('exit code: 0');
	expect(toolkit?.content).toContain('{"count": 2, "label": "a b"}');

	// Reflection wrote no version for a run with nothing new, but the playbook counts the script call and the learning's hit
	expect((await waitForReflection(page.request, second.runId)).reflectionVersion).toBeNull();
	const playbook = (await (await page.request.get(`/api/jobs/${job.id}/playbook`)).json()) as {
		version: number;
		content: {
			learnings: { hits: number }[];
			toolkit: { stats: { calls: number; failures: number } }[];
		};
	};
	expect(playbook.version).toBe(1);
	expect(playbook.content.toolkit[0].stats).toEqual({ calls: 1, failures: 0 });
	expect(playbook.content.learnings[0].hits).toBe(1);
});

test('Learning from one run works when the job does not learn automatically', async ({ page }) => {
	const job = await createLearningJob(page.request, 'Manual learning job', false);
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		finish('Done without learning')
	]);
	expect(status).toBe('succeeded');
	expect(
		((await (await page.request.get(`/api/runs/${runId}`)).json()) as { reflection: string })
			.reflection
	).toBe('skipped');

	// The button reflects once, on request
	await runUtil.scriptModel(page.request, [
		reflection('Remembered the edge case', [
			{ op: 'add_learning', kind: 'fact', text: 'The list is empty on weekends' }
		])
	]);
	await page.goto(`/runs/${runId}`);
	await page.getByRole('button', { name: 'Learn from this run' }).first().click();
	await expect(page.getByText('What this run taught the job')).toBeVisible();
	await expect(page.getByText('Remembered the edge case')).toBeVisible();
	await expect(page.getByRole('link', { name: 'playbook version 1' })).toBeVisible();

	// The job itself still doesn't learn on its own
	const detail = (await (await page.request.get(`/api/jobs/${job.id}`)).json()) as {
		selfImprove: boolean;
	};
	expect(detail.selfImprove).toBe(false);
});

test('Rolling back a learned Dockerfile restores the environment of later runs', async ({
	page
}) => {
	const job = await createLearningJob(page.request, 'Environment learning job');

	// Reflection moves an install into the job image
	const first = await runUtil.runScripted(page.request, job.id, [
		finish('Installed by hand'),
		reflection('Moved the install into the Dockerfile', [
			{
				op: 'set_dockerfile',
				content: `FROM ${SANDBOX_IMAGE}\nRUN echo preinstalled > /opt/learned.txt`
			}
		])
	]);
	expect(first.status).toBe('succeeded');
	expect((await waitForReflection(page.request, first.runId)).reflectionVersion).toBe(1);

	// The next run waits for the image and finds the preinstalled file
	const withImage = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'bash', args: { command: 'cat /opt/learned.txt' } }] },
		finish('Used the image'),
		reflection('Nothing new', [])
	]);
	expect(withImage.status).toBe('succeeded');
	expect((await toolResults(page.request, withImage.runId))[0].content).toContain('preinstalled');
	await waitForReflection(page.request, withImage.runId);

	// Rolling back to the empty playbook brings back the base image
	const rollback = await page.request.post(`/api/jobs/${job.id}/playbook/rollback`, {
		data: { version: 0 }
	});
	expect(rollback.ok()).toBeTruthy();
	const restored = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'bash', args: { command: 'cat /opt/learned.txt || echo missing' } }] },
		finish('Used the base image'),
		reflection('Nothing new', [])
	]);
	expect(restored.status).toBe('succeeded');
	expect((await toolResults(page.request, restored.runId))[0].content).toContain('missing');
	const run = (await (await page.request.get(`/api/runs/${restored.runId}`)).json()) as {
		imageRef: string;
		mode: string;
	};
	expect(run.imageRef).toBe(SANDBOX_IMAGE);
	expect(run.mode).toBe('explore');
});
