import { expect, test, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import { runStatus } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import { card, gotoListening, pageHeaderMeta } from '../utils/ui.util';

// Scheduled and webhook runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Only comes due at 03:00 UTC on New Year's Day, so the real scheduler never fires it while a test runs
const RARE_CRON = '0 3 1 1 *';

// The job's runs newest first, so the first one is the run the latest trigger created
async function listJobRuns(request: APIRequestContext, jobId: string) {
	return runUtil.listRuns(request, { job: jobId, sort: '-number' });
}

test('A run the schedule fires says the schedule started it, and the job keeps its next run', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Nightly digest', {
		cron: RARE_CRON,
		timezone: 'UTC'
	});
	expect(job.nextRunAt).toBeGreaterThan(Date.now());

	// The schedule comes due and its run finishes
	await runUtil.scriptModel(page.request, [runUtil.finish('Posted the digest')]);
	await runUtil.fireSchedule(page.request, job.id);
	const [run] = (await listJobRuns(page.request, job.id)).items;
	expect(await runUtil.waitForRun(page.request, run.id)).toBe('succeeded');

	// The run says the schedule started it, and nobody in particular
	expect(await runUtil.getRun(page.request, run.id)).toMatchObject({
		number: 1,
		trigger: 'schedule',
		triggeredBy: null,
		summary: 'Posted the digest'
	});
	await page.goto(`/runs/${run.id}`);
	await expect(page.getByText('Triggered by the schedule')).toBeVisible();
	await expect(runStatus(page)).toHaveText('Succeeded');

	// Firing leaves the next run on the following occurrence instead of clearing it, which is still the same New Year's morning
	expect((await runUtil.getJob(page.request, job.id)).nextRunAt).toBe(job.nextRunAt);
});

test("A scheduled trigger while a run is active is recorded as skipped, and the job's runs show it live", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Nightly digest', {
		cron: RARE_CRON,
		timezone: 'UTC'
	});

	// A first scheduled run keeps the job busy
	await runUtil.scriptModel(page.request, runUtil.loopScript(60, 'Should never get here'));
	await runUtil.fireSchedule(page.request, job.id);
	const [busy] = (await listJobRuns(page.request, job.id)).items;
	await runUtil.waitUntilLooping(page.request, busy.id);

	// The skipped run reaches the open list as a single live event, so the page has to be listening first
	await gotoListening(page, `/jobs/${job.id}/runs`);
	const table = page.getByRole('table', { name: 'Runs of Nightly digest' });
	await expect(table.getByRole('row', { name: /#1/ })).toContainText('Running');

	// The next occurrence while it runs is recorded as skipped under the default Skip policy
	await runUtil.fireSchedule(page.request, job.id);
	await expect(table.getByRole('row', { name: /#2/ })).toContainText('Skipped');
	const [skipped] = (await listJobRuns(page.request, job.id)).items;
	expect(skipped).toMatchObject({
		number: 2,
		status: 'skipped',
		trigger: 'schedule',
		error: 'Skipped because another run of this job was still active'
	});
	expect((await runUtil.getRun(page.request, busy.id)).status).toBe('running');
});

test('Invalid cron expressions are refused next to the field, and the job stays unscheduled until a valid one is saved', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Cron job');
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	const save = schedule.getByRole('button', { name: 'Save', exact: true });
	const cron = schedule.getByLabel('Cron expression');

	// Turning the schedule on starts from a common preset, read back in plain words
	await schedule.getByRole('switch', { name: 'Run on a schedule' }).click();
	await expect(cron).toHaveValue('0 9 * * *');
	await expect(cron).toHaveAccessibleDescription('Daily at 09:00');

	// A missing field is flagged while typing, and the server refuses it with the parser's reason
	await cron.fill('0 8 * *');
	await expect(cron).toHaveAccessibleDescription('Not a valid cron expression, e.g. 0 9 * * 1-5');
	await save.click();
	await expect(cron).toHaveAccessibleDescription(
		/^is not a valid cron expression: expected exactly 5 fields, found 4/
	);
	await expect(cron).toHaveAttribute('aria-invalid', 'true');
	await expect(save).toBeEnabled();

	// An expression that parses but never comes due is refused as well
	await cron.fill('0 9 30 2 *');
	await save.click();
	await expect(cron).toHaveAccessibleDescription('never fires');

	// The timezone has its own field, so an expression may not carry one
	await cron.fill('CRON_TZ=UTC 0 9 * * *');
	await save.click();
	await expect(cron).toHaveAccessibleDescription(
		'must not set a timezone, use the timezone field instead'
	);

	// Intervals would allow schedules down to the second, so only five fields and descriptors are accepted
	await cron.fill('@every 5m');
	await save.click();
	await expect(cron).toHaveAccessibleDescription(
		'must be a five-field expression or a descriptor such as @daily'
	);
	await expect(page.getByText('Changes saved')).toHaveCount(0);
	expect(await runUtil.getJob(page.request, job.id)).toMatchObject({
		cron: null,
		nextRunAt: null
	});
	await expect(pageHeaderMeta(page)).toContainText('On demand');

	// A valid expression saves, and the job gets its schedule and a next run
	await cron.fill('0 9 * * 1-5');
	await saveForm(schedule);
	await expect(page.getByText('Changes saved')).toBeVisible();
	await expect(cron).toHaveAccessibleDescription('Weekdays at 09:00');
	await expect(cron).not.toHaveAttribute('aria-invalid', 'true');
	await expect(pageHeaderMeta(page)).toContainText('Weekdays at 09:00');
	const saved = await runUtil.getJob(page.request, job.id);
	expect(saved.cron).toBe('0 9 * * 1-5');
	expect(saved.nextRunAt).toBeGreaterThan(Date.now());
});

test("The API refuses a timezone that is not an IANA name, including the server's own Local", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Zoned job', {
		cron: RARE_CRON,
		timezone: 'UTC'
	});

	// Local would follow whatever zone the replica runs in, so it is refused like a made-up zone
	for (const timezone of ['Mars/Olympus', 'Local']) {
		const response = await page.request.patch(`/api/jobs/${job.id}`, {
			data: { cron: RARE_CRON, timezone }
		});
		expect(response.status()).toBe(400);
		expect(await response.json()).toMatchObject({
			code: 'validation_failed',
			fields: [{ field: 'timezone', message: 'is not a known IANA timezone' }]
		});
	}

	// The job keeps the schedule it had
	expect(await runUtil.getJob(page.request, job.id)).toMatchObject({
		cron: RARE_CRON,
		nextRunAt: job.nextRunAt
	});
});

test('An @every interval is flagged while typing, since the scheduler refuses it', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Interval job');
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	await schedule.getByRole('switch', { name: 'Run on a schedule' }).click();

	// An interval gets the same warning as any other expression the scheduler refuses, before anything is saved
	const cron = schedule.getByLabel('Cron expression');
	await cron.fill('@every 5m');
	await expect(cron).toHaveAccessibleDescription('Not a valid cron expression, e.g. 0 9 * * 1-5');
});

test('Editing a refused cron expression clears its error and reads the new expression back', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Cron job');
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	await schedule.getByRole('switch', { name: 'Run on a schedule' }).click();

	// The server refuses an expression that never comes due
	const cron = schedule.getByLabel('Cron expression');
	await cron.fill('0 9 30 2 *');
	await schedule.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(cron).toHaveAccessibleDescription('never fires');

	// Fixing the expression replaces the stale error with the new expression in plain words
	await cron.fill('0 9 * * 1-5');
	await expect(cron).toHaveAccessibleDescription('Weekdays at 09:00');
	await expect(cron).not.toHaveAttribute('aria-invalid', 'true');
});

test('The Webhook card generates a token without asking, asks before rotating, and a rotated token stops working at once', async ({
	page,
	playwright,
	baseURL
}) => {
	const job = await runUtil.createJob(page.request, 'Hook job');

	// Every token the card shows comes from its own request, so counting them tells whether Cancel rotated anyway
	let rotations = 0;
	page.on('request', (request) => {
		const path = new URL(request.url()).pathname;
		if (request.method() === 'POST' && path === `/api/jobs/${job.id}/webhook-token`) rotations++;
	});

	await page.goto(`/jobs/${job.id}/settings`);
	const webhook = card(page, 'Webhook');
	const tokenDialog = page.getByRole('dialog', { name: 'Webhook token' });

	// Webhook callers are external systems without the browser session
	const external = await playwright.request.newContext({ baseURL });
	const callWebhook = (token: string) =>
		external.post(`/hooks/${job.id}`, { headers: { Authorization: `Bearer ${token}` } });
	try {
		// A job starts without a token, so its webhook refuses every call
		await expect(webhook.getByText('No token')).toBeVisible();
		await expect(webhook.getByLabel('URL', { exact: true })).toHaveValue(
			`${baseURL}/hooks/${job.id}`
		);
		expect((await callWebhook('umh_guess')).status()).toBe(401);

		// The first token needs no confirmation and shows once, together with a ready-made call
		await webhook.getByRole('button', { name: 'Generate token' }).click();
		await expect(tokenDialog).toBeVisible();
		await expect(page.getByRole('alertdialog')).toHaveCount(0);
		const first = await tokenDialog.getByRole('textbox', { name: 'Token' }).inputValue();
		expect(first).toMatch(/^umh_/);
		await expect(tokenDialog.locator('pre')).toContainText(
			`curl -X POST ${baseURL}/hooks/${job.id}`
		);
		await expect(tokenDialog.locator('pre')).toContainText(`Authorization: Bearer ${first}`);
		await tokenDialog.getByRole('button', { name: 'Done' }).click();
		await expect(tokenDialog).toBeHidden();
		await expect(webhook.getByText('Token set')).toBeVisible();

		// Rotating asks first, and cancelling closes the question without a new token
		await webhook.getByRole('button', { name: 'Rotate token' }).click();
		const confirm = page.getByRole('alertdialog');
		await expect(confirm).toContainText('Rotate the webhook token');
		await confirm.getByRole('button', { name: 'Cancel' }).click();
		await expect(confirm).toBeHidden();
		await expect(tokenDialog).toBeHidden();

		// Confirming shows a new token, and it is the only one asked for since the first, so Cancel rotated nothing
		await webhook.getByRole('button', { name: 'Rotate token' }).click();
		await page.getByRole('alertdialog').getByRole('button', { name: 'Rotate' }).click();
		await expect(tokenDialog).toBeVisible();
		const second = await tokenDialog.getByRole('textbox', { name: 'Token' }).inputValue();
		expect(second).toMatch(/^umh_/);
		expect(second).not.toBe(first);
		expect(rotations).toBe(2);
		await tokenDialog.getByRole('button', { name: 'Done' }).click();

		// The old token is refused right away, while the new one starts a run
		expect((await callWebhook(first)).status()).toBe(401);
		await runUtil.scriptModel(page.request, []);
		const accepted = await callWebhook(second);
		expect(accepted.status()).toBe(200);
		const { runId } = (await accepted.json()) as { runId: string };
		expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
		expect((await runUtil.getRun(page.request, runId)).trigger).toBe('webhook');

		// The token was only shown once, so neither a reload nor the jobs API brings it back
		await page.reload();
		await expect(webhook.getByText('Token set')).toBeVisible();
		await expect(tokenDialog).toHaveCount(0);
		expect(JSON.stringify(await runUtil.getJob(page.request, job.id))).not.toContain(second);
		expect(await (await page.request.get('/api/jobs')).text()).not.toContain(second);
	} finally {
		await external.dispose();
	}
});

test('Deleting a job from the Danger zone confirms first, cancels its queued run, lets the active one finish and keeps its runs', async ({
	page
}) => {
	const doomed = await runUtil.createJob(page.request, 'Doomed job', { concurrency: 'queue' });
	await runUtil.createJob(page.request, 'Survivor job');

	// Cancelling the confirmation keeps the job
	await page.goto(`/jobs/${doomed.id}/settings`);
	await page.getByRole('button', { name: 'Delete job' }).click();
	const confirm = page.getByRole('alertdialog');
	await expect(confirm).toContainText('Delete Doomed job');
	await confirm.getByRole('button', { name: 'Cancel' }).click();
	await expect(confirm).toBeHidden();
	await expect(page).toHaveURL(`/jobs/${doomed.id}/settings`);
	expect((await page.request.get(`/api/jobs/${doomed.id}`)).status()).toBe(200);

	// One run is busy and a second one waits behind it under the Queue policy
	await runUtil.scriptModel(
		page.request,
		runUtil.loopScript(12, 'Finished after the job was deleted')
	);
	const active = await runUtil.startRun(page.request, doomed.id);
	await runUtil.waitUntilLooping(page.request, active);
	const queued = await runUtil.startRun(page.request, doomed.id);
	expect((await runUtil.getRun(page.request, queued)).status).toBe('queued');

	// Confirming deletes the job and goes back to the list, which still has the other job
	await page.getByRole('button', { name: 'Delete job' }).click();
	await confirm.getByRole('button', { name: 'Delete job' }).click();
	await expect(page.getByText('Deleted "Doomed job"')).toBeVisible();
	await expect(page).toHaveURL('/jobs');
	const jobs = page.getByRole('table', { name: 'Jobs' });
	await expect(jobs.getByRole('row', { name: /Survivor job/ })).toBeVisible();
	await expect(jobs.getByRole('row', { name: /Doomed job/ })).toHaveCount(0);
	expect((await page.request.get(`/api/jobs/${doomed.id}`)).status()).toBe(404);

	// The queued run can never start now, so it was cancelled, while the active run goes on
	expect(await runUtil.waitForRun(page.request, queued)).toBe('cancelled');
	expect(await runUtil.getRun(page.request, queued)).toMatchObject({
		status: 'cancelled',
		error: 'Cancelled before it started',
		startedAt: null
	});
	expect((await runUtil.getRun(page.request, active)).status).toBe('running');

	// Its runs stay in the history, and the active run still finishes there with its summary
	await page.goto('/runs');
	const runs = page.getByRole('table', { name: 'Runs' });
	await expect(runs.getByRole('row', { name: /Doomed job #2/ })).toContainText('Cancelled');
	await runs.getByRole('link', { name: 'Doomed job #1' }).click();
	await expect(page).toHaveURL(`/runs/${active}`);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Doomed job #1');
	await expect(runStatus(page)).toHaveText('Succeeded', { timeout: 30_000 });
	expect(await runUtil.getRun(page.request, active)).toMatchObject({
		status: 'succeeded',
		summary: 'Finished after the job was deleted'
	});

	// Its runs can no longer be retried, since there is no job to run
	const retry = await page.request.post(`/api/runs/${active}/retry`);
	expect(retry.status()).toBe(404);
	expect(await retry.json()).toMatchObject({ code: 'not_found', message: 'Job not found' });
	expect((await listJobRuns(page.request, doomed.id)).total).toBe(2);
});

test('A deleted job starts nothing through the API, its webhook or its schedule', async ({
	page,
	playwright,
	baseURL
}) => {
	const job = await runUtil.createJob(page.request, 'Doomed job', {
		cron: RARE_CRON,
		timezone: 'UTC'
	});
	const rotated = await page.request.post(`/api/jobs/${job.id}/webhook-token`);
	expect(rotated.ok()).toBeTruthy();
	const { token } = (await rotated.json()) as { token: string };
	expect((await page.request.delete(`/api/jobs/${job.id}`)).ok()).toBeTruthy();

	// The job is gone for the API, so it can't be run from there
	expect((await page.request.get(`/api/jobs/${job.id}`)).status()).toBe(404);
	expect((await page.request.post(`/api/jobs/${job.id}/runs`, { data: {} })).status()).toBe(404);

	// Its webhook refuses the job's token as if it were wrong
	const external = await playwright.request.newContext({ baseURL });
	try {
		const hook = await external.post(`/hooks/${job.id}`, {
			headers: { Authorization: `Bearer ${token}` }
		});
		expect(hook.status()).toBe(401);
	} finally {
		await external.dispose();
	}

	// An occurrence of its schedule that comes due anyway starts no run
	await runUtil.fireSchedule(page.request, job.id);
	expect((await listJobRuns(page.request, job.id)).total).toBe(0);
});

test('A run of a deleted job offers no Retry', async ({ page }) => {
	// A finished run whose job is deleted afterwards
	const job = await runUtil.createJob(page.request, 'Gone job');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, []);
	expect(status).toBe('succeeded');
	expect((await page.request.delete(`/api/jobs/${job.id}`)).ok()).toBeTruthy();

	// Its page offers no Retry, which could only fail
	await page.goto(`/runs/${runId}`);
	await expect(runStatus(page)).toHaveText('Succeeded');
	await expect(
		page.getByRole('button', { name: 'Retry', exact: true, disabled: false })
	).toHaveCount(0);
});
