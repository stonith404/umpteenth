import { expect, test, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import { runHeader, runStatus } from '../utils/run-view.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

// The reset also cancels runs a failed test left busy, so they can't take the next test's scripted answers
test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Opens the job's Run now dialog from its header and submits it without extra instructions or input
async function runNow(page: Page) {
	await page
		.locator('[data-slot="page-header-actions"]')
		.getByRole('button', { name: 'Run now' })
		.click();
	const dialog = page.getByRole('dialog', { name: 'Run now' });
	await dialog.getByRole('button', { name: 'Run now' }).click();
	await expect(dialog).toBeHidden();
}

// The ID of the run whose page is open, which Run now and Retry navigate to
function runIdFromUrl(page: Page) {
	return new URL(page.url()).pathname.split('/').pop()!;
}

test("Under the default Skip policy a trigger during an active run is recorded as a skipped run that the job's stats leave out, and a retry only runs once the active run is gone", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Skip job');
	const first = await runUtil.startBusyRun(page.request, job.id);

	// The Schedule card shows the default policy
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	await expect(schedule.getByLabel('When runs overlap')).toHaveText('Skip');

	// Run now while the first run is busy is declined but recorded, and the dialog says so
	await runNow(page);
	await expect(page.getByText('Run skipped', { exact: true })).toBeVisible();
	await expect(page.getByText('Another run of this job is still active.')).toBeVisible();
	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
	const skipped = runIdFromUrl(page);

	// The skipped run never started, so it says why and has neither events nor a Stop or Learn action
	// Skipping is what the policy asked for, so the alert takes the neutral tone of a cancelled run rather than the red of a failure
	await expect(page.getByRole('heading', { level: 1 })).toContainText('#2');
	await expect(runStatus(page)).toHaveText('Skipped');
	const alert = runHeader(page).getByRole('alert');
	await expect(alert.getByText('Skipped', { exact: true })).toBeVisible();
	await expect(alert).toContainText('Skipped because another run of this job was still active');
	await expect(alert).toHaveClass(/(^|\s)text-foreground\/70(\s|$)/);
	await expect(page.getByText('No events')).toBeVisible();
	await expect(runHeader(page).getByRole('button', { name: 'Retry' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Learn from this run' })).toHaveCount(0);
	expect(await runUtil.getRun(page.request, skipped)).toMatchObject({
		status: 'skipped',
		trigger: 'manual',
		error: 'Skipped because another run of this job was still active',
		startedAt: null,
		finishedAt: expect.any(Number),
		turns: 0
	});
	expect((await page.request.post(`/api/runs/${skipped}/learn`)).status()).toBe(409);

	// Retrying while the first run is still busy is skipped as well and opens the new skipped run
	await runHeader(page).getByRole('button', { name: 'Retry' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByText('Retry skipped', { exact: true })).toBeVisible();
	await expect(page.getByRole('heading', { level: 1 })).toContainText('#3');
	await expect(runStatus(page)).toHaveText('Skipped');
	expect(await runUtil.getRun(page.request, runIdFromUrl(page))).toMatchObject({
		status: 'skipped',
		trigger: 'retry'
	});

	// The first run is still busy after both skips, and once it is gone a retry really runs
	expect((await runUtil.getRun(page.request, first)).status).toBe('running');
	expect(await runUtil.cancelRun(page.request, first)).toBe('cancelled');
	await runUtil.scriptModel(page.request, [runUtil.finish('Ran after the first one')]);
	await runHeader(page).getByRole('button', { name: 'Retry' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByRole('heading', { level: 1 })).toContainText('#4');
	await expect(runStatus(page)).toHaveText('Succeeded', { timeout: 30_000 });
	expect(await runUtil.getRun(page.request, runIdFromUrl(page))).toMatchObject({
		status: 'succeeded',
		trigger: 'retry',
		summary: 'Ran after the first one'
	});

	// The job's history keeps the skipped runs, while its stats leave them out
	await runHeader(page).getByRole('link', { name: 'Skip job', exact: true }).click();
	await page.getByRole('tab', { name: 'Runs' }).click();
	const table = page.getByRole('table', { name: 'Runs of Skip job' });
	await expect(table.getByRole('row', { name: /#1/ })).toContainText('Cancelled');
	await expect(table.getByRole('row', { name: /#2/ })).toContainText('Skipped');
	await expect(table.getByRole('row', { name: /#3/ })).toContainText('Skipped');
	await expect(table.getByRole('row', { name: /#4/ })).toContainText('Succeeded');
	const stats = await (await page.request.get(`/api/jobs/${job.id}/stats`)).json();
	expect(stats.runCount).toBe(2);
	const charted = stats.runs.map((run: { number: number }) => run.number);
	expect(charted.sort((a: number, b: number) => a - b)).toEqual([1, 4]);
});

test('Under the Queue policy chosen in the Schedule card a trigger during an active run waits as queued, can be stopped before it starts, and starts once the active run ends', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Queue job');

	// Switch the policy to Queue in the Schedule card, which decides what the next trigger does
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	await schedule.getByLabel('When runs overlap').click();
	await page.getByRole('option', { name: /^Queue/ }).click();
	await expect(schedule.getByLabel('When runs overlap')).toHaveText('Queue');
	await saveForm(schedule);
	const saved = await (await page.request.get(`/api/jobs/${job.id}`)).json();
	expect(saved.concurrency).toBe('queue');

	// Run now while the first run is busy queues the new run behind it instead of skipping it
	const first = await runUtil.startBusyRun(page.request, job.id);
	await runNow(page);
	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
	const queued = runIdFromUrl(page);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('#2');

	// The queued run waits on its page and can be stopped, but not retried
	await expect(runStatus(page)).toHaveText('Queued');
	await expect(page.getByText('Waiting in the queue…')).toBeVisible();
	await expect(runHeader(page).getByRole('button', { name: 'Stop', exact: true })).toBeVisible();
	await expect(runHeader(page).getByRole('button', { name: 'Retry' })).toHaveCount(0);
	expect(await runUtil.getRun(page.request, queued)).toMatchObject({
		status: 'queued',
		startedAt: null
	});

	// A third trigger lines up behind it, which the API's queued answer only tells apart from a skip, and the dashboard lists both waiting runs as queued
	const third = await runUtil.triggerRun(page.request, job.id);
	expect(third.status).toBe('queued');
	await page.goto('/');
	const runningNow = page.getByTestId('running-now');
	await expect(runningNow.getByRole('link', { name: /Queue job\s*#2/ })).toContainText('Queued');
	await expect(runningNow.getByRole('link', { name: /Queue job\s*#3/ })).toContainText('Queued');

	// Stopping a queued run ends it at once, before it ever started
	await page.goto(`/runs/${third.runId}`);
	await runHeader(page).getByRole('button', { name: 'Stop', exact: true }).click();
	const stopDialog = page.getByRole('alertdialog');
	await expect(stopDialog).toContainText('Queue job #3');
	await stopDialog.getByRole('button', { name: 'Stop run' }).click();
	await expect(page.getByText('Stopping the run')).toBeVisible();
	await expect(runStatus(page)).toHaveText('Cancelled');
	await expect(runHeader(page).getByRole('alert')).toContainText('Cancelled before it started');
	await expect(page.getByTestId('run-timeline')).toContainText('Cancelled');
	await expect(runHeader(page).getByRole('button', { name: 'Retry' })).toBeVisible();
	expect(await runUtil.getRun(page.request, third.runId)).toMatchObject({
		status: 'cancelled',
		error: 'Cancelled before it started',
		startedAt: null,
		turns: 0
	});
	expect((await page.request.post(`/api/runs/${third.runId}/cancel`)).status()).toBe(409);

	// The first run is untouched, and once it ends the queued run starts on its own and its page follows live
	await page.goto(`/runs/${queued}`);
	await expect(runStatus(page)).toHaveText('Queued');
	expect((await runUtil.getRun(page.request, first)).status).toBe('running');
	await runUtil.scriptModel(page.request, [runUtil.finish('Queued run done')]);
	expect(await runUtil.cancelRun(page.request, first)).toBe('cancelled');
	await expect(runStatus(page)).toHaveText('Succeeded', { timeout: 30_000 });
	const firstRun = await runUtil.getRun(page.request, first);
	const queuedRun = await runUtil.getRun(page.request, queued);
	expect(queuedRun.summary).toBe('Queued run done');
	expect(queuedRun.startedAt).toBeGreaterThanOrEqual(firstRun.finishedAt!);
});

test('Under the Parallel policy a trigger during an active run starts a second run right away', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Parallel job', { concurrency: 'parallel' });

	// The Schedule card shows the Parallel policy and explains how overlapping runs start
	await page.goto(`/jobs/${job.id}/settings`);
	const schedule = page.getByRole('form', { name: 'Schedule' });
	await expect(schedule.getByLabel('When runs overlap')).toHaveText('Parallel');
	await expect(
		schedule.getByText('Every trigger starts a run right away, even if others are active.')
	).toBeVisible();

	// Both runs get the same long command, since runs sharing the fake model take its answers in any order
	await page.goto(`/jobs/${job.id}`);
	const [busy] = runUtil.loopScript(60, 'Should never get here');
	await runUtil.scriptModel(page.request, [busy, busy]);
	const first = await runUtil.triggerRun(page.request, job.id);
	await runUtil.waitForStatus(page.request, first.runId, 'running');

	// Run now starts the second run while the first one is still busy, and its command runs too
	await runNow(page);
	await expect(page.getByText('Started a run of "Parallel job"')).toBeVisible();
	await expect(page.getByRole('heading', { level: 1 })).toContainText('#2');
	const second = runIdFromUrl(page);
	await expect(page.getByRole('region', { name: 'Output of bash' })).toContainText('tick', {
		timeout: 20_000
	});
	await expect(runStatus(page)).toHaveText('Running');
	expect((await runUtil.getRun(page.request, first.runId)).status).toBe('running');

	// The runs table shows both running side by side, while the sidebar counts the job once
	await page.goto('/runs');
	const rows = page.getByRole('table', { name: 'Runs' }).getByRole('row', { name: /Parallel job/ });
	await expect(rows.filter({ hasText: 'Running' })).toHaveCount(2);
	await expect(page.getByLabel('1 running')).toBeVisible();

	// Neither run ends on its own, so both are still live when they are stopped
	expect(await runUtil.cancelRun(page.request, first.runId)).toBe('cancelled');
	expect(await runUtil.cancelRun(page.request, second)).toBe('cancelled');
});
