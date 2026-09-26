import { expect, test, type Locator, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { bashOutput, runDetail, runHeader, runStatus, timelineStep } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import { card, definition, gotoListening } from '../utils/ui.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Clicks a link or button that downloads a file and returns the name the browser suggests and the file's text
async function download(page: Page, trigger: Locator) {
	const [file] = await Promise.all([page.waitForEvent('download'), trigger.click()]);
	return { name: file.suggestedFilename(), text: await readFile(await file.path(), 'utf8') };
}

test('Retrying a run starts a new run with the same input and instructions and opens it', async ({
	page
}) => {
	// The retry has to name the signed-in user as the one who started it
	const me = (await (await page.request.get('/api/users/me')).json()) as { id: string };

	// The first run fails, which is when someone reaches for Retry
	const job = await runUtil.createJob(page.request, 'Weather job');
	await runUtil.scriptModel(page.request, [
		{
			toolCalls: [
				{ name: 'finish', args: { status: 'failure', summary: 'The weather API was down' } }
			]
		}
	]);
	const { runId: firstId } = await runUtil.triggerRun(page.request, job.id, {
		input: { city: 'Berlin', days: 3 },
		instructions: 'Only report **rain**.'
	});
	expect(await runUtil.waitForRun(page.request, firstId)).toBe('failed');

	// The header says who started the run, and the Outputs tab shows what it was given
	await page.goto(`/runs/${firstId}?tab=outputs`);
	const header = runHeader(page);
	await expect(header).toContainText('Triggered by you');
	const input = card(page, 'Input').locator('[data-slot="json-view"]');
	await expect(input).toContainText('"city": "Berlin"');
	await expect(input).toContainText('"days": 3');
	await expect(card(page, 'Run instructions').locator('strong')).toHaveText('rain');
	await expect(page.getByText('No structured outputs or artifacts')).toBeVisible();

	// Cancelling the confirmation starts nothing and stays on the run
	await header.getByRole('button', { name: 'Retry' }).click();
	const dialog = page.getByRole('alertdialog');
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toBeHidden();
	await expect(page).toHaveURL(`/runs/${firstId}?tab=outputs`);
	const runs = await page.request.get('/api/runs', { params: { job: job.id } });
	expect(((await runs.json()) as { total: number }).total).toBe(1);

	// Confirming opens the new run, which reads the same input from /ump/input.json
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('cat /ump/input.json', { status: 'success', summary: 'No rain' })
	);
	await header.getByRole('button', { name: 'Retry' }).click();
	await dialog.getByRole('button', { name: 'Retry' }).click();
	await expect(page).toHaveURL(new RegExp(`/runs/(?!${firstId})[0-9a-f-]+$`));
	const retryId = new URL(page.url()).pathname.split('/').pop()!;
	await expect(page).toHaveTitle('Weather job #2 · Runs · Umpteenth');
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Weather job #2');
	await expect(header).toContainText('Retried by you');
	await expect(page.getByRole('tab', { name: 'Timeline' })).toHaveAttribute(
		'aria-selected',
		'true'
	);
	await expect(bashOutput(page, 'cat /ump/input.json')).toContainText(/"city":\s*"Berlin"/, {
		timeout: 20_000
	});
	await expect(runStatus(page)).toHaveText('Succeeded', { timeout: 30_000 });

	// The retry carries the input and instructions over, while the first run stays as it was
	const retry = await (await page.request.get(`/api/runs/${retryId}`)).json();
	expect(retry).toMatchObject({
		number: 2,
		trigger: 'retry',
		triggeredBy: me.id,
		status: 'succeeded',
		input: { city: 'Berlin', days: 3 },
		instructions: 'Only report **rain**.'
	});
	expect(await runUtil.getRun(page.request, firstId)).toMatchObject({
		trigger: 'manual',
		status: 'failed',
		summary: 'The weather API was down'
	});
	await page.getByRole('tab', { name: 'Outputs' }).click();
	await expect(card(page, 'Input').locator('[data-slot="json-view"]')).toContainText(
		'"city": "Berlin"'
	);
	await expect(card(page, 'Run instructions')).toContainText('Only report rain.');
});

test('Files in /ump/outputs and ump outputs show on the Outputs tab and download under their names', async ({
	page
}) => {
	// write_file and bash both write into /ump/outputs, and finish overrides one of the outputs `ump output set` stored
	const job = await runUtil.createJob(page.request, 'Report job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{
			toolCalls: [
				{
					name: 'write_file',
					args: { path: '/ump/outputs/report.csv', content: 'city,temp\nBerlin,21\n' }
				},
				{
					name: 'bash',
					args: {
						command: [
							'mkdir -p /ump/outputs/charts',
							`printf '%s' '{"points":3}' > /ump/outputs/charts/data.json`,
							'ump output set rows 42',
							`ump output set label 'hello world'`,
							`ump output set meta '{"source":"api"}'`,
							'ump output set count 1'
						].join(' && ')
					}
				}
			]
		},
		runUtil.finish('Wrote the report', { count: 3 })
	]);
	expect(status).toBe('succeeded');
	await page.goto(`/runs/${runId}`);

	// The timeline shows the write with its path, every ump call, and the collected files as downloads
	const write = timelineStep(page, 'write_file');
	await expect(write.getByText('/ump/outputs/report.csv', { exact: true })).toBeVisible();
	await expect(write.getByRole('region', { name: 'Result of write_file' })).toHaveText(
		'Wrote 20 bytes to /ump/outputs/report.csv.'
	);
	await expect(timelineStep(page, 'POST /v1/output')).toHaveCount(4);
	const collected = timelineStep(page, 'Collected 2 artifact(s)');
	const fromTimeline = await download(page, collected.getByRole('link', { name: 'report.csv' }));
	expect(fromTimeline).toEqual({ name: 'report.csv', text: 'city,temp\nBerlin,21\n' });

	// The Outputs tab lists both files with their sizes and downloads them under their own names
	await page.getByRole('tab', { name: 'Outputs' }).click();
	const artifacts = page.getByRole('list', { name: 'Artifacts' }).getByRole('listitem');
	await expect(artifacts).toHaveCount(2);
	await expect(artifacts.filter({ hasText: 'report.csv' })).toContainText('20 B');
	await expect(artifacts.filter({ hasText: 'charts/data.json' })).toContainText('12 B');
	const fromOutputs = await download(
		page,
		page.getByRole('link', { name: 'Download charts/data.json' })
	);
	expect(fromOutputs).toEqual({ name: 'data.json', text: '{"points":3}' });

	// The outputs of ump and finish are merged, where finish wins for a key both set
	const outputs = page.locator('[data-slot="outputs"]');
	await expect(definition(outputs, 'rows')).toHaveText('42');
	await expect(definition(outputs, 'label')).toHaveText('hello world');
	await expect(definition(outputs, 'meta')).toContainText('"source": "api"');
	await expect(definition(outputs, 'count')).toHaveText('3');
	await expect(page.getByText('No input or run instructions')).toBeVisible();
	const run = (await (await page.request.get(`/api/runs/${runId}`)).json()) as { outputs: unknown };
	expect(run.outputs).toEqual({
		rows: 42,
		label: 'hello world',
		meta: { source: 'api' },
		count: 3
	});

	// Paths are cleaned inside the run's artifacts folder, so climbing up to the folder that holds every run's files finds nothing
	const artifact = (path: string) =>
		page.request.get(`/api/runs/${runId}/artifact`, { params: { path } });
	const json = await artifact('charts/data.json');
	expect(json.headers()['content-type']).toBe('application/json');
	expect(json.headers()['content-disposition']).toBe('attachment; filename=data.json');
	expect(await (await artifact('/charts/../report.csv')).text()).toBe('city,temp\nBerlin,21\n');
	expect((await artifact(`../../${runId}/artifacts/report.csv`)).status()).toBe(404);
	expect((await artifact('nope.txt')).status()).toBe(404);
});

test('Several tool calls in one turn show as separate steps with their exit codes, JSON results and errors', async ({
	page
}) => {
	// One turn makes five calls, then a finish with an invalid status fails before the right one ends the run
	const job = await runUtil.createJob(page.request, 'Toolbox job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{
			text: 'Checking a few things at once.',
			toolCalls: [
				{ name: 'bash', args: { command: 'echo one' } },
				{ name: 'bash', args: { command: 'echo two >&2; exit 2' } },
				{ name: 'state_set', args: { key: 'cursor', value: '{"page":2,"done":false}' } },
				{ name: 'state_get', args: { key: 'cursor' } },
				{ name: 'lookup_weather', args: { city: 'Berlin' } }
			]
		},
		{ toolCalls: [{ name: 'finish', args: { status: 'done', summary: 'All good' } }] },
		runUtil.finish('Checked everything')
	]);
	expect(status).toBe('succeeded');
	await page.goto(`/runs/${runId}`);

	// Every call is a step of its own, in the order the model made them
	await expect(
		page.getByTestId('run-timeline').locator('[data-slot="timeline-step"]')
	).toContainText([
		'Checking a few things at once.',
		'echo one',
		'echo two',
		'state_set',
		'state_get',
		'lookup_weather',
		'finish needs',
		'Checked everything'
	]);

	// Only the failing command gets an exit badge, and its stderr shows as output
	const one = timelineStep(page, 'echo one');
	await expect(one.getByRole('region', { name: 'Output of bash' })).toHaveText('$ echo one one');
	await expect(one.locator('[data-slot="badge"]')).toHaveCount(0);
	const two = timelineStep(page, 'echo two');
	await expect(two.locator('[data-slot="badge"]')).toHaveText('exit 2');
	await expect(two.getByRole('region', { name: 'Output of bash' })).toHaveText(
		'$ echo two >&2; exit 2 two'
	);

	// A JSON result is pretty-printed next to the arguments it answers
	const get = timelineStep(page, 'state_get');
	await expect(get.getByText('cursor', { exact: true })).toBeVisible();
	const result = get.getByRole('region', { name: 'Result of state_get' });
	await expect(result).toContainText('"page": 2');
	await expect(result).toContainText('"done": false');

	// An unknown tool and a malformed finish stay on the timeline as errors, and the right finish ends the run
	const unknown = timelineStep(page, 'lookup_weather');
	await expect(unknown).toContainText('error');
	await expect(unknown.getByRole('region', { name: 'Result of lookup_weather' })).toHaveText(
		'Unknown tool "lookup_weather". Use one of the tools you were given.'
	);
	const badFinish = timelineStep(page, 'finish needs');
	await expect(badFinish).toContainText('error');
	await expect(badFinish.getByRole('region', { name: 'Result of finish' })).toHaveText(
		'finish needs `status` (success or failure) and `summary`.'
	);
	await expect(timelineStep(page, 'Finished')).toContainText('Checked everything');
	await expect(runStatus(page)).toHaveText('Succeeded');
	await expect(runDetail(page, 'Turns')).toHaveText('3');

	// The waterfall names the failing command's exit code next to the command
	await page.getByRole('tab', { name: 'Waterfall' }).click();
	await expect(page.getByTestId('run-waterfall')).toContainText('echo two >&2; exit 2 · exit 2');
});

test('A turn streams its reasoning live and keeps it collapsed once persisted', async ({
	page
}) => {
	// A short reasoning and an answer long enough to start collapsed once persisted
	const reasoning = 'The file list is short, so one summary is enough.';
	const answer = Array.from(
		{ length: 40 },
		(_, i) => `Finding ${i + 1} holds up on a second look.`
	).join(' ');

	// The second turn waits before it answers, so its reasoning and text stream for a few seconds after the page has connected
	// The last turn waits too, so the run is still going when the second turn is persisted
	const job = await runUtil.createJob(page.request, 'Thinking job');
	await runUtil.scriptModel(page.request, [
		{
			text: 'Let me look around first.',
			toolCalls: [{ name: 'bash', args: { command: 'echo ready' } }]
		},
		{
			reasoning,
			text: answer,
			toolCalls: [{ name: 'bash', args: { command: 'echo done' } }],
			delayMs: 6000
		},
		{ ...runUtil.finish('Done'), delayMs: 5000 }
	]);
	const runId = await runUtil.startRun(page.request, job.id);
	await page.goto(`/runs/${runId}`);

	// The turn in progress shows the reasoning and then the text as they stream
	const liveTurn = page.getByTestId('live-turn');
	await expect(liveTurn).toContainText(reasoning, { timeout: 30_000 });
	await expect(timelineStep(page, 'streaming')).toContainText('Turn 2');
	await expect(liveTurn).toContainText('Finding 1 holds up');

	// The persisted turn replaces the live one while the run still goes on, rather than only once it ended
	await expect(liveTurn).toHaveCount(0, { timeout: 10_000 });
	const turn = timelineStep(page, 'Turn 2');
	const reasoningToggle = turn.getByRole('button', { name: `Reasoning ${reasoning.length} chars` });
	await expect(reasoningToggle).toBeVisible();
	expect((await runUtil.getRun(page.request, runId)).status).toBe('running');
	await expect(runStatus(page)).toHaveText('Succeeded', { timeout: 30_000 });

	// Reasoning starts collapsed and opens to the exact text
	await expect(reasoningToggle).toHaveAttribute('aria-expanded', 'false');
	await expect(turn.getByText(reasoning)).toBeHidden();
	await reasoningToggle.click();
	await expect(turn.getByText(reasoning)).toBeVisible();

	// A long answer starts collapsed too, while a short one shows inline
	const responseToggle = turn.getByRole('button', {
		name: `Response ${answer.length.toLocaleString('en-US')} chars`
	});
	await expect(responseToggle).toHaveAttribute('aria-expanded', 'false');
	await expect(turn.getByText('Finding 40 holds up')).toBeHidden();
	await responseToggle.click();
	await expect(turn.getByText('Finding 40 holds up')).toBeVisible();
	const firstTurn = timelineStep(page, 'Turn 1');
	await expect(firstTurn.getByText('Let me look around first.')).toBeVisible();
	await expect(firstTurn.getByRole('button', { name: /^(Reasoning|Response)\b/ })).toHaveCount(0);

	// The reasoning is stored with the model call
	const events = (await (await page.request.get(`/api/runs/${runId}/events`)).json()) as {
		type: string;
		payload: { turn?: number; reasoning?: string };
	}[];
	const call = events.find((e) => e.type === 'llm.call' && e.payload.turn === 2);
	expect(call?.payload.reasoning).toBe(reasoning);
});

test('The Raw tab exports the run with its events, and tab deep links survive a reload', async ({
	page
}) => {
	// The copy is read back from the clipboard, which the browser only allows with these permissions
	await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);

	// A finished run with a few events to export
	const job = await runUtil.createJob(page.request, 'Raw job');
	const { runId } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('echo raw', { status: 'success', summary: 'Raw done' })
	);

	// The API's own view of the finished run is what the export must reproduce
	const run = await (await page.request.get(`/api/runs/${runId}`)).json();
	const events = (await (await page.request.get(`/api/runs/${runId}/events`)).json()) as unknown[];

	// A link straight to the Raw tab shows the run and all of its events
	await page.goto(`/runs/${runId}?tab=raw`);
	await expect(page.getByRole('tab', { name: 'Raw' })).toHaveAttribute('aria-selected', 'true');
	await expect(
		page.getByText(`The run and its ${events.length} events as returned by the API.`)
	).toBeVisible();
	await expect(page.locator('[data-slot="json-view"]')).toContainText('"jobName": "Raw job"');

	// The download holds exactly what the API returns
	const file = await download(page, page.getByRole('button', { name: 'Download JSON' }));
	expect(file.name).toBe('run-raw-job-1.json');
	expect(JSON.parse(file.text)).toEqual({ run, events });

	// Copying puts the same JSON on the clipboard
	await page.getByRole('button', { name: 'Copy' }).click();
	await expect(page.getByText('Copied the run as JSON')).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(file.text);

	// An unknown tab falls back to the timeline
	await page.goto(`/runs/${runId}?tab=nonsense`);
	await expect(page.getByRole('tab', { name: 'Timeline' })).toHaveAttribute(
		'aria-selected',
		'true'
	);
	await expect(page.getByTestId('run-timeline')).toBeVisible();

	// The chosen tab lives in the URL, so it survives a reload
	await page.getByRole('tab', { name: 'Waterfall' }).click();
	await expect(page).toHaveURL(`/runs/${runId}?tab=waterfall`);
	await page.reload();
	await expect(page.getByRole('tab', { name: 'Waterfall' })).toHaveAttribute(
		'aria-selected',
		'true'
	);
	await expect(page.getByTestId('run-waterfall')).toBeVisible();

	// The timeline is the default, so choosing it drops the parameter
	await page.getByRole('tab', { name: 'Timeline' }).click();
	await expect(page).toHaveURL(`/runs/${runId}`);
	await expect(page.getByTestId('run-timeline')).toBeVisible();
});

test('A run is not found from another workspace, neither on its page nor through the API', async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// The run keeps a file that must not leak to another workspace
	const job = await runUtil.createJob(page.request, 'Secret job');
	const { runId } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('echo s3cr3t > /ump/outputs/secret.txt', {
			status: 'success',
			summary: 'Stored'
		})
	);
	const owned = await page.request.get(`/api/runs/${runId}/artifact`, {
		params: { path: 'secret.txt' }
	});
	expect(await owned.text()).toBe('s3cr3t\n');

	// Bob signs in without an invite, so he owns a workspace of his own and passes every role check
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	await bob.goto(`/runs/${runId}`);
	await expect(bob.getByRole('heading', { name: 'Run not found' })).toBeVisible();

	// Every endpoint of the run says the run doesn't exist, rather than getting further and failing on something else such as its job
	const endpoints = [
		['GET', `/api/runs/${runId}`],
		['GET', `/api/runs/${runId}/events`],
		['GET', `/api/runs/${runId}/stream`],
		['GET', `/api/runs/${runId}/artifacts`],
		['GET', `/api/runs/${runId}/artifact?path=secret.txt`],
		['POST', `/api/runs/${runId}/retry`],
		['POST', `/api/runs/${runId}/cancel`],
		['POST', `/api/runs/${runId}/learn`],
		['DELETE', `/api/runs/${runId}`]
	];
	for (const [method, url] of endpoints) {
		const response = await bob.request.fetch(url, { method, timeout: 10_000 });
		expect(response.status(), `${method} ${url}`).toBe(404);
		expect(await response.json(), `${method} ${url}`).toMatchObject({
			code: 'not_found',
			message: 'Run not found'
		});
	}
	const bulk = await bob.request.post('/api/runs/delete', { data: { ids: [runId] } });
	expect(await bulk.json()).toEqual({ deleted: [], skipped: [runId] });
	expect(((await (await bob.request.get('/api/runs')).json()) as { total: number }).total).toBe(0);

	// The run is untouched for its own workspace
	expect((await runUtil.getRun(page.request, runId)).status).toBe('succeeded');
	const runs = await page.request.get('/api/runs', { params: { job: job.id } });
	expect(((await runs.json()) as { total: number }).total).toBe(1);
	await bob.context().close();
});

test('A run deleted elsewhere turns into the not-found page', async ({ page }) => {
	// A finished run, since only those can be deleted
	const job = await runUtil.createJob(page.request, 'Doomed job');
	const { runId } = await runUtil.runScripted(page.request, job.id, [runUtil.finish('Done')]);

	// The page learns about the delete from the workspace's live events, so it has to be listening first
	await gotoListening(page, `/runs/${runId}`);
	await expect(runStatus(page)).toHaveText('Succeeded');

	// A mark on the window survives only as long as the page isn't reloaded
	await page.evaluate(() => ((window as unknown as { marked: boolean }).marked = true));

	// Someone deletes the run in another tab, and the open page follows without a reload
	expect((await page.request.delete(`/api/runs/${runId}`)).ok()).toBeTruthy();
	await expect(page.getByRole('heading', { name: 'Run not found' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Back to runs' })).toBeVisible();
	await expect(runHeader(page)).toHaveCount(0);
	await expect(page).toHaveURL(`/runs/${runId}`);
	expect(await page.evaluate(() => (window as unknown as { marked?: boolean }).marked)).toBe(true);
});
