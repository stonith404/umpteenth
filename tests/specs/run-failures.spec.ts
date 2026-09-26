import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { runDetail, runHeader, runStatus, timelineStep } from '../utils/run-view.util';
import runUtil, { type ScriptedResponse } from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The agent's finish call for a run it reports as failed, with a summary that says why
function failingFinish(summary: string): ScriptedResponse {
	return { toolCalls: [{ name: 'finish', args: { status: 'failure', summary } }] };
}

// Every model call of the run, including the failed attempts of a repeated one
async function modelCalls(request: APIRequestContext, runId: string) {
	return (await runUtil.getEvents(request, runId)).filter((e) => e.type === 'llm.call');
}

// The timeline steps whose title is exactly the text, for titles such as Error that other steps mention too
function timelineStepTitled(page: Page, title: string) {
	return page
		.getByTestId('run-timeline')
		.locator('[data-slot="timeline-step"]')
		.filter({ has: page.getByText(title, { exact: true }) });
}

test('A run the agent reported as failed explains why in its header and hides the restated error', async ({
	page
}) => {
	// The agent reports the failure with a summary that says why
	const job = await runUtil.createJob(page.request, 'Failing job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		failingFinish('## Could not reach the API\n- the server answered **503**')
	]);
	expect(status).toBe('failed');
	expect((await runUtil.getRun(page.request, runId)).error).toBe('the agent reported failure');

	// The header explains the failure with the agent's summary, rendered as Markdown, instead of the generic reason
	await page.goto(`/runs/${runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	const alert = runHeader(page).getByRole('alert');
	await expect(alert.getByRole('heading', { name: 'Could not reach the API' })).toBeVisible();
	await expect(alert.locator('strong')).toHaveText('503');

	// The runner restates the reported failure as an error event, which the finish step already says, so the timeline leaves it out
	await expect(timelineStep(page, 'Finished with a failure')).toContainText(
		'Could not reach the API'
	);
	await expect(timelineStepTitled(page, 'Error')).toHaveCount(0);
	const errors = (await runUtil.getEvents(page.request, runId)).filter((e) => e.type === 'error');
	expect(errors.map((e) => e.payload.message)).toEqual(['the agent reported failure']);
});

test("Learning from a failed run can be retried after reflection failed, and the run can't be deleted meanwhile", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Failing job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		failingFinish('The API was down')
	]);
	expect(status).toBe('failed');

	// Learning starts at once and moves to the Learned tab, where the answer is held back so the pending state can be seen
	await page.goto(`/runs/${runId}`);
	await runUtil.scriptModel(page.request, [
		{ error: 'the reflection model is down', delayMs: 3000 }
	]);
	const header = runHeader(page);
	await header.getByRole('button', { name: 'Learn from this run' }).click();
	await expect(page.getByText('Learning from this run')).toBeVisible();
	await expect(page).toHaveURL(/tab=learned/);
	await expect(page.getByText('Reflecting on this run')).toBeVisible();

	// Learning still reads the run, so neither the page nor the API deletes it until that is done
	const deleteButton = header.getByRole('button', { name: 'Delete', exact: true });
	await expect(deleteButton).toBeDisabled();
	expect((await page.request.delete(`/api/runs/${runId}`)).status()).toBe(409);

	// The failed reflection says why, and the run can be deleted again
	const reflectionFailed = page.getByRole('alert').filter({ hasText: 'Reflection failed' });
	await expect(reflectionFailed).toContainText(
		/the reflection model failed: the reflection model is down/i,
		{ timeout: 15_000 }
	);
	await expect(deleteButton).toBeEnabled();
	const failedReflection = await runUtil.getRun(page.request, runId);
	expect(failedReflection.reflection).toBe('failed');
	expect(failedReflection.reflectionError).toBe(
		'the reflection model failed: the reflection model is down'
	);

	// Learning again with a working model replaces the failure with what the run taught the job
	await runUtil.scriptModel(page.request, [runUtil.reflection('Nothing worth keeping')]);
	await page.getByRole('button', { name: 'Learn again' }).click();
	await expect(page.getByText('Nothing worth keeping')).toBeVisible({ timeout: 15_000 });
	await expect(reflectionFailed).toHaveCount(0);
	const learned = await runUtil.waitForReflection(page.request, runId);
	expect(learned.reflection).toBe('done');
	expect(learned.reflectionVersion).toBeNull();
	expect(learned.reflectionError).toBeNull();
});

test('A rejected model call fails the run with its error, an overloaded model is asked again and the run goes on, and the runs list finds the failed run by its error', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Flaky model job');

	// A rejected API key fails the same way every time, so the first failed call ends the run without a retry
	const rejected = await runUtil.runScripted(page.request, job.id, [
		{ error: 'invalid x-api-key', errorStatus: 401 }
	]);
	expect(rejected.status).toBe('failed');
	const rejectedRun = await runUtil.getRun(page.request, rejected.runId);
	expect(rejectedRun.error).toBe(
		'model call failed: fake API error (status 401): invalid x-api-key'
	);
	expect(rejectedRun.turns).toBe(1);
	expect(rejectedRun.cost).toBe(0);
	expect(await modelCalls(page.request, rejected.runId)).toHaveLength(1);

	// The page names the failed call in the header, on the turn and as the run's error
	await page.goto(`/runs/${rejected.runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	const alert = runHeader(page).getByRole('alert');
	await expect(alert.getByText('Failed', { exact: true })).toBeVisible();
	await expect(alert).toContainText(
		'Model call failed: fake API error (status 401): invalid x-api-key'
	);
	await expect(timelineStep(page, 'Turn 1')).toContainText(
		'Fake API error (status 401): invalid x-api-key'
	);
	await expect(timelineStepTitled(page, 'Error')).toContainText(
		'Model call failed: fake API error (status 401): invalid x-api-key'
	);

	// A run without a single answer used nothing, which a price of $0.00 would misstate
	await expect(runDetail(page, 'Cost')).toHaveText('—');

	// An overloaded model is asked again after a short wait, and its answer goes on as the same turn
	const retried = await runUtil.runScripted(page.request, job.id, [
		{ error: 'Overloaded', errorStatus: 529 },
		runUtil.finish('Answered on the second try')
	]);
	expect(retried.status).toBe('succeeded');
	const retriedRun = await runUtil.getRun(page.request, retried.runId);
	expect(retriedRun.turns).toBe(1);
	expect(retriedRun.error).toBeNull();
	expect(retriedRun.msTotal).toBeGreaterThanOrEqual(2000);
	const calls = await modelCalls(page.request, retried.runId);
	expect(calls.map((c) => c.payload.turn)).toEqual([1, 1]);
	expect(calls[0].payload.error).toBe('fake API error (status 529): Overloaded');
	expect(calls[1].payload.error).toBeUndefined();

	// The timeline shows the failed attempt next to the answer, and the repeated call still counts as one turn
	await page.goto(`/runs/${retried.runId}`);
	await expect(runStatus(page)).toHaveText('Succeeded');
	const attempts = timelineStep(page, 'Turn 1');
	await expect(attempts).toHaveCount(2);
	await expect(attempts.first()).toContainText('Fake API error (status 529): Overloaded');
	await expect(attempts.last()).not.toContainText('Overloaded');
	await expect(timelineStep(page, 'Finished')).toContainText('Answered on the second try');
	await expect(runDetail(page, 'Turns')).toHaveText('1');

	// Runs are found by their error, so searching for it lists only the failed run
	await page.goto('/runs');
	await page.getByRole('searchbox', { name: 'Search runs' }).fill('x-api-key');
	await expect(page).toHaveURL(/search=x-api-key/);
	const table = page.getByRole('table', { name: 'Runs' });
	const failedRow = table
		.getByRole('row')
		.filter({ has: page.getByRole('checkbox', { name: 'Select Flaky model job #1' }) });
	await expect(failedRow).toContainText('Failed');
	await expect(table.getByRole('checkbox', { name: 'Select Flaky model job #2' })).toBeHidden();
});

test('A repeated model call stays the same turn while the run is live', async ({ page }) => {
	// The repeated answer streams slowly and runs a short command, so the live page can be seen during turn 1
	const job = await runUtil.createJob(page.request, 'Retried job');
	await runUtil.scriptModel(page.request, [
		{ error: 'Overloaded', errorStatus: 529 },
		{
			text: 'Trying again now that the model answers.',
			toolCalls: [{ name: 'bash', args: { command: 'sleep 3; echo done' } }],
			delayMs: 3000
		},
		runUtil.finish('Done after the retry')
	]);
	const runId = await runUtil.startRun(page.request, job.id);
	await page.goto(`/runs/${runId}`);

	// The answer that streams after the failed attempt belongs to turn 1
	const liveTurn = page
		.getByTestId('run-timeline')
		.locator('[data-slot="timeline-step"]', { has: page.getByTestId('live-turn') });
	await expect(liveTurn).toContainText('Turn 1', { timeout: 20_000 });

	// While the command of turn 1 runs, the run has taken one turn
	await expect(timelineStep(page, 'sleep 3')).toContainText('running', { timeout: 20_000 });
	await expect(runDetail(page, 'Turns')).toHaveText('1');

	// Once the run ends, the header shows the run's two turns, the repeated one and the finish
	expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
	expect((await runUtil.getRun(page.request, runId)).turns).toBe(2);
	await expect(runDetail(page, 'Turns')).toHaveText('2');
});

test('A model that ends its turn without calling finish is reminded once, then the run fails, and a refusal fails it right away', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Forgetful job');

	// An answer without a tool gets a reminder that finish is required, and a finish after it ends the run as usual
	const reminded = await runUtil.runScripted(page.request, job.id, [
		{ text: 'All done, the report is ready.' },
		runUtil.finish('Wrapped up after the reminder')
	]);
	expect(reminded.status).toBe('succeeded');
	const remindedRun = await runUtil.getRun(page.request, reminded.runId);
	expect(remindedRun.turns).toBe(2);
	expect(remindedRun.summary).toBe('Wrapped up after the reminder');

	// The page shows the answer without a tool, the turn after the reminder and the finish
	await page.goto(`/runs/${reminded.runId}`);
	await expect(runStatus(page)).toHaveText('Succeeded');
	await expect(timelineStep(page, 'Turn 1')).toContainText('All done, the report is ready.');
	await expect(timelineStep(page, 'Turn 2')).toBeVisible();
	await expect(timelineStep(page, 'Finished')).toContainText('Wrapped up after the reminder');
	await expect(runDetail(page, 'Turns')).toHaveText('2');

	// A second answer without a tool fails the run, whatever made the model stop
	const stopped = await runUtil.runScripted(page.request, job.id, [
		{ text: 'Here is part of the answer', stop: 'max_tokens' },
		{ text: 'Still thinking about it.' },
		runUtil.finish('Should never get here')
	]);
	expect(stopped.status).toBe('failed');
	const stoppedRun = await runUtil.getRun(page.request, stopped.runId);
	expect(stoppedRun.error).toBe('the model stopped without calling finish');
	expect(stoppedRun.turns).toBe(2);
	expect(stoppedRun.summary).toBeNull();
	expect((await runUtil.getEvents(page.request, stopped.runId)).map((e) => e.type)).not.toContain(
		'finish'
	);

	// The turn that hit the output limit says so, and the run explains why it failed
	await page.goto(`/runs/${stopped.runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	await expect(timelineStep(page, 'Turn 1')).toContainText('stopped: max_tokens');
	const secondTurn = timelineStep(page, 'Turn 2');
	await expect(secondTurn).toContainText('Still thinking about it.');
	await expect(secondTurn).not.toContainText('stopped:');
	await expect(runHeader(page).getByRole('alert')).toContainText(
		'The model stopped without calling finish'
	);
	await expect(timelineStepTitled(page, 'Error')).toContainText(
		'The model stopped without calling finish'
	);

	// A refusal ends the run at once, without a reminder that would let the queued finish through
	const refused = await runUtil.runScripted(page.request, job.id, [
		{ text: "I can't help with that.", stop: 'refusal' },
		runUtil.finish('Should never get here')
	]);
	expect(refused.status).toBe('failed');
	const refusedRun = await runUtil.getRun(page.request, refused.runId);
	expect(refusedRun.error).toBe('the model refused to continue');
	expect(refusedRun.turns).toBe(1);
	expect((await runUtil.getEvents(page.request, refused.runId)).map((e) => e.type)).not.toContain(
		'finish'
	);

	// The refused turn says why the model stopped, and it stays the only turn
	await page.goto(`/runs/${refused.runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	await expect(timelineStep(page, 'Turn 1')).toContainText('stopped: refusal');
	await expect(timelineStep(page, 'Turn 1')).toContainText("I can't help with that.");
	await expect(timelineStep(page, 'Turn 2')).toHaveCount(0);
	await expect(runHeader(page).getByRole('alert')).toContainText('The model refused to continue');
	await expect(runDetail(page, 'Turns')).toHaveText('1');
});

test('A script that calls ump fail fails the run with its reason, even though the agent finished successfully', async ({
	page
}) => {
	// The command reports a failure through the broker, and the agent then finishes as if all went well
	const job = await runUtil.createJob(page.request, 'Upstream job');
	const { runId, status } = await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('ump fail "the upstream API returned 503"', {
			status: 'success',
			summary: 'All good'
		})
	);
	expect(status).toBe('failed');
	const run = await runUtil.getRun(page.request, runId);
	expect(run.error).toBe('the upstream API returned 503');
	expect(run.summary).toBe('All good');

	// The header gives the script's reason, since the agent's summary does not say why the run failed
	await page.goto(`/runs/${runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	await expect(runHeader(page).getByRole('alert')).toContainText('The upstream API returned 503');

	// ump fail exits non-zero and its broker call is on the timeline
	await expect(timelineStep(page, 'ump fail').getByText('exit 1', { exact: true })).toBeVisible();
	await expect(timelineStep(page, 'POST /v1/fail')).toBeVisible();

	// The run's error comes after the successful finish, since the runner only fails the run once the agent is done
	await expect(timelineStepTitled(page, 'Error')).toContainText('The upstream API returned 503');
	await expect(
		page.getByTestId('run-timeline').locator('[data-slot="timeline-step"]')
	).toContainText(['ump fail', 'Finished', 'Error']);
});
