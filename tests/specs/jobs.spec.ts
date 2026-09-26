import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// Saving and running a job uses a real sandbox, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend();
	await authUtil.authenticate(page);
});

// The spec the fake utility model answers the compile step with
const compiledSpec = {
	title: 'Stale PR digest',
	goal: 'Post a digest of stale pull requests',
	schedule: { cron: '0 8 * * 1-5', timezone: 'Europe/Berlin', human: 'Weekdays at 08:00' },
	successCriteria: ['All open PRs are considered', 'Exactly one message is posted'],
	inputs: [],
	outputs: [{ name: 'count', type: 'integer', description: 'Number of stale PRs' }],
	mcp: [{ server: 'slack', why: 'post the digest' }],
	network: 'internet',
	dockerfile: null,
	sideEffects: ['Posts to Slack'],
	warnings: ['Which Slack channel should it post to?']
};

test('Create a job by compiling a description, then save and run it', async ({ page }) => {
	// The first answer compiles the description, the second one finishes the first run
	await runUtil.scriptModel(page.request, [
		{ text: JSON.stringify(compiledSpec), delayMs: 1500 },
		{ toolCalls: [{ name: 'finish', args: { status: 'success', summary: 'Posted the digest' } }] }
	]);

	await page.goto('/jobs');
	await page.getByRole('link', { name: 'New job' }).first().click();
	await expect(page).toHaveURL('/jobs/new');

	// Compile the description and watch the loading state
	await page
		.getByRole('textbox', { name: 'Describe the job' })
		.fill('Every weekday at 8:00 Berlin time, post stale PRs of acme/api to #eng.');
	await page.getByRole('button', { name: 'Compile' }).click();
	await expect(page.getByText('Compiling your job')).toBeVisible();

	// The compiled spec shows up as editable cards
	await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Stale PR digest', {
		timeout: 15_000
	});
	await expect(page.getByText('Which Slack channel should it post to?')).toBeVisible();
	await expect(page.getByLabel('Cron expression')).toHaveValue('0 8 * * 1-5');
	await expect(page.getByRole('textbox', { name: 'Success criterion 2' })).toHaveValue(
		'Exactly one message is posted'
	);
	await expect(page.getByText('Not configured')).toBeVisible();

	// Edit the name before saving, then save and follow the first run
	await page.getByLabel('Name', { exact: true }).fill('Stale PRs of acme/api');
	await page.getByRole('button', { name: 'Save & run' }).click();
	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/, { timeout: 15_000 });

	const runId = page.url().split('/').pop()!;
	expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');

	// The job keeps the compiled schedule and the edited name
	await page.goto('/jobs');
	const row = page.getByRole('table', { name: 'Jobs' }).getByRole('row', {
		name: /Stale PRs of acme\/api/
	});
	await expect(row).toContainText('Weekdays at 08:00');
	await expect(row).toContainText('Succeeded');
});

test('A failed compile can be skipped by filling in the spec by hand', async ({ page }) => {
	await runUtil.scriptModel(page.request, [{ text: 'this is not a spec' }]);

	await page.goto('/jobs/new');
	await page.getByRole('textbox', { name: 'Describe the job' }).fill('Say hello every hour.');
	await page.getByRole('button', { name: 'Compile' }).click();
	await expect(page.getByText('Compiling failed')).toBeVisible({ timeout: 15_000 });

	await page.getByRole('button', { name: 'Fill in manually' }).click();
	await page.getByLabel('Name', { exact: true }).fill('Hello job');
	await page.getByRole('button', { name: 'Save', exact: true }).click();

	await expect(page).toHaveURL(/\/jobs\/[0-9a-f-]+$/);
	await expect(page.getByRole('heading', { level: 1, name: 'Hello job' })).toBeVisible();
	await expect(page.getByText('On demand').first()).toBeVisible();
});

test('Edit job settings including the schedule', async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Settings job');

	await page.goto(`/jobs/${job.id}`);
	await page.getByRole('tab', { name: 'Settings' }).click();
	await expect(page).toHaveURL(`/jobs/${job.id}/settings`);

	// Rename the job, give it a schedule and override one limit
	await page.getByLabel('Name', { exact: true }).fill('Renamed job');
	await page.getByRole('switch', { name: 'Run on a schedule' }).click();
	await page.getByLabel('Cron expression').fill('30 9 * * 1');
	await page.getByLabel('Timeout').fill('1200');
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page.getByText('Changes saved')).toBeVisible();

	// The header picks up the new name and the schedule in plain words
	await expect(page.getByRole('heading', { level: 1, name: 'Renamed job' })).toBeVisible();
	await expect(page.getByText('Mondays at 09:30').first()).toBeVisible();

	// Everything survives a reload
	await page.reload();
	await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Renamed job');
	await expect(page.getByLabel('Cron expression')).toHaveValue('30 9 * * 1');
	await expect(page.getByLabel('Timeout')).toHaveValue('1200');

	const response = await page.request.get(`/api/jobs/${job.id}`);
	const saved = await response.json();
	expect(saved.cron).toBe('30 9 * * 1');
	expect(saved.limits).toEqual({ timeoutSeconds: 1200 });
	expect(saved.spec.schedule.human).toBe('Mondays at 09:30');
	expect(saved.nextRunAt).toBeGreaterThan(Date.now());

	// Removing the schedule makes the job run on demand again
	await page.getByRole('switch', { name: 'Run on a schedule' }).click();
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page.getByText('Changes saved')).toBeVisible();
	await expect(page.getByText('On demand').first()).toBeVisible();
});
