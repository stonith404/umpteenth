import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('A scripted run streams its timeline live and ends with outputs', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Timeline job');
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('for i in $(seq 1 8); do echo tick $i; sleep 1; done', {
			status: 'success',
			summary: '## Done\n- counted **3** files',
			outputs: { count: 3, report: 'ok' }
		})
	);
	const runId = await runUtil.startRun(page.request, job.id);
	await page.goto(`/runs/${runId}`);

	// The command's output streams into the pending bash step before its result is persisted
	const timeline = page.getByTestId('run-timeline');
	const bashStep = timeline.locator('[data-slot="timeline-step"]', { hasText: 'for i in' });
	await expect(bashStep.getByRole('region', { name: 'Output of bash' })).toContainText('tick 2', {
		timeout: 20_000
	});
	await expect(bashStep).toContainText('running');
	await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeVisible();

	// The persisted result replaces the preview and the run succeeds without a reload
	const header = page.locator('header', { has: page.getByRole('heading', { level: 1 }) });
	await expect(header.locator('[data-slot="status-badge"]').first()).toHaveText('Succeeded', {
		timeout: 30_000
	});
	// A clean exit shows no badge, so the settled step is recognized by the running indicator going away
	await expect(bashStep).not.toContainText('running');
	await expect(bashStep.getByRole('region', { name: 'Output of bash' })).toContainText('tick 8');
	await expect(timeline.getByRole('heading', { name: 'Done' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();

	// The outputs tab shows the summary and the structured outputs, and keeps the tab in the URL
	await page.getByRole('tab', { name: 'Outputs' }).click();
	await expect(page).toHaveURL(/tab=outputs/);
	const outputs = page.locator('[data-slot="outputs"]');
	await expect(outputs).toContainText('count');
	await expect(outputs).toContainText('3');
	await expect(outputs).toContainText('report');

	// The waterfall draws the timed steps of the run
	await page.getByRole('tab', { name: 'Waterfall' }).click();
	await expect(page.getByTestId('run-waterfall')).toContainText('Turn 1');
	await expect(page.getByTestId('run-waterfall')).toContainText('bash');
});

test('A running run can be cancelled from its page', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Long job');
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('for i in $(seq 1 60); do echo tick $i; sleep 1; done', {
			status: 'success',
			summary: 'Should never get here'
		})
	);
	const runId = await runUtil.startRun(page.request, job.id);
	await page.goto(`/runs/${runId}`);

	// Wait until the command is actually running, so the cancel reaches a live sandbox
	await expect(page.getByRole('region', { name: 'Output of bash' })).toContainText('tick', {
		timeout: 20_000
	});

	// The timeline follows the output and scrolls the header away, so the live bar takes over its Stop
	// Clicking the header's Stop instead races the hand-over, since Playwright keeps waiting for the hidden button to come back
	const header = page.locator('header', { has: page.getByRole('heading', { level: 1 }) });
	const liveBar = page.locator('[data-slot="run-live-bar"]');
	await expect(liveBar).toContainText('Long job #1', { timeout: 20_000 });
	await expect(header.getByRole('button', { name: 'Stop', exact: true })).toBeHidden();

	await liveBar.getByRole('button', { name: 'Stop', exact: true }).click();
	const dialog = page.getByRole('alertdialog');
	await expect(dialog).toContainText('Long job #1');
	await dialog.getByRole('button', { name: 'Stop run' }).click();

	await expect(header.locator('[data-slot="status-badge"]').first()).toHaveText('Cancelled', {
		timeout: 20_000
	});
	await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
	expect(await runUtil.waitForRun(page.request, runId)).toBe('cancelled');
});

test('The runs table filters by status and search, keeping both in the URL', async ({ page }) => {
	const alpha = await runUtil.createJob(page.request, 'Alpha report');
	const beta = await runUtil.createJob(page.request, 'Beta digest');
	await runUtil.runScripted(
		page.request,
		alpha.id,
		runUtil.bashThenFinish('echo alpha', { status: 'success', summary: 'Alpha is fine' })
	);
	await runUtil.runScripted(
		page.request,
		beta.id,
		runUtil.bashThenFinish('echo beta; exit 3', { status: 'failure', summary: 'Beta broke' })
	);

	await page.goto('/runs');
	const table = page.getByRole('table', { name: 'Runs' });
	const alphaRow = table.getByRole('row', { name: /Alpha report/ });
	const betaRow = table.getByRole('row', { name: /Beta digest/ });
	await expect(alphaRow).toContainText('Succeeded');
	await expect(betaRow).toContainText('Failed');

	// Filter by status through the faceted filter
	await page.getByRole('button', { name: 'Status' }).click();
	await page.getByRole('option', { name: 'Failed' }).click();
	await page.keyboard.press('Escape');
	await expect(page).toHaveURL(/status=failed/);
	await expect(betaRow).toBeVisible();
	await expect(alphaRow).toBeHidden();

	// The filter survives a reload because the URL is the state
	await page.reload();
	await expect(betaRow).toBeVisible();
	await expect(alphaRow).toBeHidden();

	// Search on the server instead
	await page.getByRole('button', { name: 'Reset' }).click();
	await expect(page).not.toHaveURL(/status=/);
	await page.getByRole('searchbox', { name: 'Search runs' }).fill('alpha');
	await expect(page).toHaveURL(/search=alpha/);
	await expect(alphaRow).toBeVisible();
	await expect(betaRow).toBeHidden();

	await page.reload();
	await expect(page.getByRole('searchbox', { name: 'Search runs' })).toHaveValue('alpha');
	await expect(betaRow).toBeHidden();

	// Rows open the run page, anywhere but their checkbox
	await alphaRow.getByText('Succeeded').click();
	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Alpha report');
});

test('The runs table shows new runs and their status changes live', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Live job');
	await page.goto('/runs');
	await expect(page.getByText('No runs yet')).toBeVisible();

	// A run started elsewhere appears on the first page without a reload and settles in place
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('sleep 2; echo done', { status: 'success', summary: 'Done' })
	);
	await runUtil.startRun(page.request, job.id);

	const row = page.getByRole('table', { name: 'Runs' }).getByRole('row', { name: /Live job/ });
	await expect(row).toBeVisible();
	await expect(row.locator('[data-slot="status-badge"]')).toHaveText(/Queued|Provisioning|Running/);
	await expect(row.locator('[data-slot="status-badge"]')).toHaveText('Succeeded', {
		timeout: 30_000
	});
});

test('Admins delete finished runs from their page and from the runs table', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Cleanup job');
	const runs: string[] = [];
	for (let i = 0; i < 3; i++) {
		const { runId } = await runUtil.runScripted(page.request, job.id, [
			runUtil.finish(`Run ${i + 1} is done`)
		]);
		runs.push(runId);
	}

	// The run page deletes one run and goes on to the job's runs
	await page.goto(`/runs/${runs[0]}`);
	await page.getByRole('button', { name: 'Delete', exact: true }).click();
	const dialog = page.getByRole('alertdialog');
	await expect(dialog).toContainText('Cleanup job #1');
	await dialog.getByRole('button', { name: 'Delete' }).click();
	await expect(page.getByText('Deleted "Cleanup job #1"')).toBeVisible();
	await expect(page).toHaveURL(new RegExp(`/jobs/${job.id}/runs`));
	expect((await page.request.get(`/api/runs/${runs[0]}`)).status()).toBe(404);

	// The table deletes the checked runs at once
	await page.goto('/runs');
	const table = page.getByRole('table', { name: 'Runs' });
	await expect(table.getByRole('row')).toHaveCount(3);
	await table.getByRole('checkbox', { name: 'Select Cleanup job #2' }).check();
	await table.getByRole('checkbox', { name: 'Select Cleanup job #3' }).check();
	await expect(page.getByText('2 selected')).toBeVisible();
	await page.getByRole('button', { name: 'Delete', exact: true }).click();
	await expect(page.getByRole('alertdialog')).toContainText('Delete 2 runs');
	await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click();
	await expect(page.getByText('Deleted 2 runs')).toBeVisible();
	await expect(page.getByText('No runs yet')).toBeVisible();
});

test('A running run cannot be deleted until it has finished', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Busy job');
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('for i in $(seq 1 60); do echo tick $i; sleep 1; done', {
			status: 'success',
			summary: 'Should never get here'
		})
	);
	const runId = await runUtil.startRun(page.request, job.id);

	// Its checkbox and its menu item are disabled, and the API refuses it too
	await page.goto('/runs');
	const table = page.getByRole('table', { name: 'Runs' });
	await expect(table.getByRole('row', { name: /Busy job/ })).toContainText('Running', {
		timeout: 20_000
	});
	await expect(table.getByRole('checkbox', { name: 'Select Busy job #1' })).toBeDisabled();
	await table.getByRole('button', { name: 'Actions for Busy job #1' }).click();
	await expect(page.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
	await page.keyboard.press('Escape');
	expect((await page.request.delete(`/api/runs/${runId}`)).status()).toBe(409);

	await page.request.post(`/api/runs/${runId}/cancel`);
	await runUtil.waitForRun(page.request, runId);
});
