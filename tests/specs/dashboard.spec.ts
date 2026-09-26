import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend();
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
