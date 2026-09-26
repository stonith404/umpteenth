import { expect, test, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import runUtil, { type ScriptedResponse } from '../utils/run.util';

const { finish, reflection } = runUtil;

// A job goes through many real sandbox runs here, so it gets more time than the suite default
test.describe.configure({ timeout: 180_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Scripts the model, runs the job with an input and waits until the run and its reflection ended
async function runWith(
	request: APIRequestContext,
	jobId: string,
	input: Record<string, unknown>,
	responses: ScriptedResponse[]
) {
	await runUtil.scriptModel(request, responses);
	const runId = await runUtil.startRun(request, jobId, input);
	await runUtil.waitForRun(request, runId, 60_000);
	return { runId, run: await runUtil.waitForReflection(request, runId) };
}

async function job(request: APIRequestContext, id: string) {
	return (await (await request.get(`/api/jobs/${id}`)).json()) as {
		nextMode: string;
		demoted: boolean;
	};
}

const topStories = `#!/usr/bin/env python3
# ump:name        top_stories
# ump:description Print the top stories as JSON
# ump:args        {"count":"integer"}
import argparse, json
parser = argparse.ArgumentParser()
parser.add_argument("--count", type=int, required=True)
print(json.dumps([{"title": f"Story {i}"} for i in range(parser.parse_args().count)]))`;

// The main script reads the story count from the run input, which a renamed field breaks
const main = `#!/usr/bin/env bash
# ump:description Report the top stories without the agent
set -euo pipefail
count=$(python3 -c "import json; print(json.load(open('/ump/input.json'))['count'])")
ump step "fetch stories"
/ump/toolkit/top_stories --count "$count" > /tmp/stories.json
ump output set count "$count"
ump summary "Reported $count stories"`;

const repairedMain = main.replace(
	"['count']",
	".get('count') or json.load(open('/ump/input.json'))['total']"
);

// A first draft of main that reads a field the input doesn't have, which its shadow run catches before it becomes the job's
const brokenMain = main.replace("['count']", "['story_count']");

test('A job graduates to a main script, falls back when it breaks, and gets repaired', async ({
	page
}) => {
	const request = page.request;
	const { id } = await runUtil.createJob(request, 'Graduating job', {
		instruction: 'Report the top stories.',
		selfImprove: true
	});
	const input = { count: 2 };

	// The first run explores and its script becomes a toolkit tool
	let { run } = await runWith(request, id, input, [
		{ toolCalls: [{ name: 'bash', args: { command: 'echo exploring' } }] },
		finish('Explored', { count: 2 }),
		reflection('Promoted the story script', [{ op: 'upsert_script', content: topStories }])
	]);
	expect(run.mode).toBe('explore');

	// Three Assisted runs take the same single step, and the third qualifies the job for graduation
	// Its first draft of main fails in a shadow run, so reflection gets a second chance before the job graduates
	const verify = JSON.stringify({ checks: ['output.count >= 1'], llm: false });
	let runId = '';
	for (const last of [false, false, true]) {
		const answers = last
			? [
					reflection('Graduated the job', [
						{ op: 'propose_main', content: brokenMain },
						{ op: 'set_verify', content: verify }
					]),
					reflection('Graduated the job', [
						{ op: 'propose_main', content: main },
						{ op: 'set_verify', content: verify }
					])
				]
			: [reflection('Nothing new')];
		({ runId, run } = await runWith(request, id, input, [
			{ toolCalls: [{ name: 'toolkit__top_stories', args: { count: 2 } }] },
			finish('Reported the stories', { count: 2 }),
			...answers
		]));
		expect(run.mode).toBe('assisted');
	}
	expect(run.reflectionOps.map((o) => `${o.op}:${o.status}`)).toEqual([
		'propose_main:applied',
		'set_verify:applied'
	]);
	expect(run.reflectionOps[0].test?.status).toBe('passed');
	expect((await job(request, id)).nextMode).toBe('scripted');

	// The run's Learned tab shows that main was tried before it was applied
	await page.goto(`/runs/${runId}?tab=learned`);
	await expect(page.getByText('Main passed its checks in a shadow run')).toBeVisible();

	// The scripted run needs no model at all, and reflection skips it
	({ run } = await runWith(request, id, input, []));
	expect(run).toMatchObject({ status: 'succeeded', mode: 'scripted', fellBack: false, turns: 0 });
	expect(run.cost).toBe(0);
	expect(run.summary).toBe('Reported 2 stories');
	expect(run.reflection).toBe('skipped');

	// A renamed input field breaks the script, the agent finishes the job, and reflection repairs main
	const broken = { total: 2 };
	const fallback = await runWith(request, id, broken, [
		finish('Finished what the script started', { count: 2 }),
		reflection('Repaired main for the renamed field', [
			{ op: 'update_main', content: repairedMain }
		])
	]);
	expect(fallback.run).toMatchObject({ status: 'succeeded', mode: 'scripted', fellBack: true });
	expect(fallback.run.reflectionOps.map((o) => `${o.op}:${o.status}`)).toEqual([
		'update_main:applied'
	]);
	expect(fallback.run.reflectionOps[0].test?.status).toBe('passed');

	// The run page shows the failed check and the handover
	await page.goto(`/runs/${fallback.runId}`);
	await expect(page.getByText('Verification failed')).toBeVisible();
	await expect(page.getByText('Fell back to the agent')).toBeVisible();
	await expect(page.getByText('Scripted · fell back')).toBeVisible();

	// The repaired script handles the new field on its own
	({ run } = await runWith(request, id, broken, []));
	expect(run).toMatchObject({ status: 'succeeded', mode: 'scripted', fellBack: false });

	// Turning graduation off puts the agent back on every run, and reflection may no longer touch main
	await page.goto(`/jobs/${id}/settings`);
	const learning = page.getByRole('form', { name: 'Learning' });
	await learning.getByRole('switch', { name: 'Graduate to a script' }).click();
	await saveForm(learning);
	expect((await job(request, id)).nextMode).toBe('assisted');
	await expect(page.getByText('Runs as')).toContainText('Assisted');
	({ run } = await runWith(request, id, broken, [
		finish('Reported the stories', { count: 2 }),
		reflection('Tried to touch main', [{ op: 'update_main', content: main }]),
		reflection('Tried to touch main', [{ op: 'update_main', content: main }])
	]));
	expect(run).toMatchObject({ status: 'succeeded', mode: 'assisted', fellBack: false });
	expect(run.reflectionOps.map((o) => `${o.op}:${o.status}`)).toEqual(['update_main:rejected']);

	// Turning it back on runs the unchanged main again
	await learning.getByRole('switch', { name: 'Graduate to a script' }).click();
	await saveForm(learning);
	expect((await job(request, id)).nextMode).toBe('scripted');

	// Two fallbacks in a row demote the job to Assisted until it graduates again
	for (let i = 0; i < 2; i++) {
		({ run } = await runWith(request, id, { nothing: true }, [
			finish('Did it by hand', { count: 1 }),
			reflection('Could not repair it yet')
		]));
		expect(run.fellBack).toBe(true);
	}
	const demoted = await job(request, id);
	expect(demoted).toMatchObject({ nextMode: 'assisted', demoted: true });
	await page.goto(`/jobs/${id}`);
	await expect(page.getByText('Demoted', { exact: true })).toBeVisible();
	await expect(page.getByText('Runs as')).toContainText('Assisted');
});
