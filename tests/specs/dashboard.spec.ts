import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil, { type ScriptedResponse } from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('The dashboard welcomes a fresh workspace and fills in once runs exist', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByTestId('dashboard-empty')).toContainText('Nothing to show yet');

	const job = await runUtil.createJob(page.request, 'Dashboard job');
	await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('echo ok', { status: 'success', summary: 'Fine' })
	);
	await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('exit 1', { status: 'failure', summary: 'Broken' })
	);

	await page.reload();
	await expect(page.getByTestId('kpi-runs')).toContainText('2');
	await expect(page.getByTestId('kpi-success-rate')).toContainText('50%');
	await expect(page.getByTestId('recent-failures')).toContainText('Dashboard job');
	await expect(page.getByText('Runs per day')).toBeVisible();

	// The range lives in the URL
	await page.getByRole('tab', { name: '30d' }).click();
	await expect(page).toHaveURL(/range=30d/);
	await expect(page.getByTestId('kpi-runs')).toContainText('2');
});

test('The command palette jumps to a run', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Palette job');
	await runUtil.runScripted(
		page.request,
		job.id,
		runUtil.bashThenFinish('echo ok', { status: 'success', summary: 'Fine' })
	);

	await page.goto('/runs');
	await expect(page.getByRole('row', { name: /Palette job/ })).toBeVisible();
	await page.keyboard.press('ControlOrMeta+k');
	const palette = page.getByRole('dialog');
	await palette.getByRole('combobox').fill('palette');
	await palette.getByRole('option', { name: /Palette job #1/ }).click();

	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Palette job');
});

test('The dashboard counts outcomes by the lifecycle rules and follows running and cancelled runs live', async ({
	page
}) => {
	// A job skips a trigger while its run is busy, which is the default overlap policy
	const busy = await runUtil.createJob(page.request, 'Busy job');
	const busyRunId = await runUtil.startBusyRun(page.request, busy.id);
	expect((await runUtil.triggerRun(page.request, busy.id)).status).toBe('skipped');

	// Only the busy run is underway, since a skipped run never started
	// The live events stream may open after the figures arrive, and a change published before it opens never reaches the page
	const liveEvents = page.waitForResponse('**/api/events');
	await page.goto('/');
	const running = page.getByTestId('running-now');
	await expect(running).toContainText('Running now');
	await expect(running).toContainText('1 run queued or in progress');
	await expect(running.getByRole('link')).toHaveText([/Busy job\s*#1/]);
	await liveEvents;

	// Cancelling the run takes the card off the dashboard without a reload
	expect(await runUtil.cancelRun(page.request, busyRunId)).toBe('cancelled');
	await expect(running).toHaveCount(0, { timeout: 10_000 });

	// A model that is down fails its run, and another job succeeds
	const broken = await runUtil.createJob(page.request, 'Broken model job');
	// The fake model fails the call with this message
	const offline: ScriptedResponse = { error: 'Model offline' };
	expect((await runUtil.runScripted(page.request, broken.id, [offline])).status).toBe('failed');
	const fine = await runUtil.createJob(page.request, 'Fine job');
	expect((await runUtil.runScripted(page.request, fine.id, [runUtil.finish('Fine')])).status).toBe(
		'succeeded'
	);

	// The cancelled run counts as a run but neither as a success nor a failure, and the skipped one doesn't count at all
	// The page loads afresh, since following runs live was already shown and only the counting is checked here
	await page.reload();
	const runs = page.getByTestId('kpi-runs');
	await expect(runs.getByText('3', { exact: true })).toBeVisible();
	await expect(runs).toContainText('1 succeeded · 1 failed');
	await expect(page.getByTestId('kpi-success-rate')).toContainText('50%');

	// Only real failures are listed, with the reason why
	const failures = page.getByTestId('recent-failures');
	await expect(failures).toContainText('Broken model job');
	await expect(failures).toContainText('Model call failed: Model offline');
	await expect(failures).not.toContainText('Busy job');

	// The chart's figures still count the skipped and the cancelled run apart
	const overview = (await (await page.request.get('/api/stats/overview?range=7d')).json()) as {
		perDay: { skipped: number; cancelled: number }[];
	};
	const sum = (key: 'skipped' | 'cancelled') =>
		overview.perDay.reduce((total, bucket) => total + bucket[key], 0);
	expect(sum('skipped')).toBe(1);
	expect(sum('cancelled')).toBe(1);
});
