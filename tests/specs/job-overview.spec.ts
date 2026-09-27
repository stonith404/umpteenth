import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { replaceCode, saveForm } from '../utils/form.util';
import runUtil from '../utils/run.util';
import { card, gotoListening, pageHeaderMeta } from '../utils/ui.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

// A schedule names its zone only when it differs from the browser's, so the browser reads times in a fixed zone
test.use({ timezoneId: 'UTC' });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The fields of GET /api/jobs/{id}/stats that specs check
type JobStats = {
	runs: { number: number; status: string; mode: string }[];
	runCount: number;
	succeeded: number;
	failed: number;
	successRate: number;
	avgTokens: number;
};

// Picks how the workspace shows usage, where tokens make averages exact since the fake model has no price
async function setUsageUnit(request: APIRequestContext, unit: 'price' | 'tokens') {
	const response = await request.patch('/api/settings', { data: { usageUnit: unit } });
	expect(response.ok()).toBeTruthy();
}

// The job's aggregated numbers and per-run points for one period, as the overview loads them
async function getStats(request: APIRequestContext, jobId: string, range: string) {
	const response = await request.get(`/api/jobs/${jobId}/stats`, { params: { range } });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as JobStats;
}

// How many runs the job has, which tells whether a refused submit still reached the backend
async function countRuns(request: APIRequestContext, jobId: string) {
	return (await runUtil.listRuns(request, { job: jobId })).total;
}

// The overview's Performance section with its period control, tiles and chart
function performanceSection(page: Page) {
	return page.getByRole('region', { name: 'Performance' });
}

// One tile of the Performance section, found by its exact label
function tile(page: Page, label: string) {
	return performanceSection(page)
		.locator('[data-slot="card"]')
		.filter({
			has: page.locator('[data-slot="card-description"]').getByText(label, { exact: true })
		});
}

// The rows of the chart's screen reader table, one per run in chronological order
function chartRows(page: Page) {
	return page.getByRole('table', { name: 'Runs in this period' }).locator('tbody').getByRole('row');
}

test('The Spec card refuses duplicate input names inline and drops blank rows when saving', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Deploy preview');
	await page.goto(`/jobs/${job.id}/settings`);
	const spec = page.getByRole('form', { name: 'Spec' });

	// Two inputs share a name, the second of another type
	await spec.getByRole('button', { name: 'Add input' }).click();
	await spec.getByRole('textbox', { name: 'Input 1 name', exact: true }).fill('repo');
	await spec
		.getByRole('textbox', { name: 'Input 1 description', exact: true })
		.fill('Repository to deploy');
	await spec.getByRole('button', { name: 'Add input' }).click();
	await spec.getByRole('textbox', { name: 'Input 2 name', exact: true }).fill('repo');
	await spec.getByRole('button', { name: 'Input 2 type', exact: true }).click();
	await page.getByRole('option', { name: 'integer', exact: true }).click();

	// One filled criterion and one left blank, which saving should drop
	await spec.getByRole('button', { name: 'Add criterion' }).click();
	await spec.getByRole('textbox', { name: 'Success criterion 1', exact: true }).fill('It responds');
	await spec.getByRole('button', { name: 'Add criterion' }).click();

	// The backend refuses the repeated name, the error shows in the Inputs set, and nothing is stored
	const save = spec.getByRole('button', { name: 'Save', exact: true });
	await save.click();
	const duplicate = spec.getByRole('group', { name: 'Inputs' }).getByRole('alert');
	await expect(duplicate).toHaveText('has more than one field named "repo"');
	await expect(save).toBeEnabled();
	expect((await (await page.request.get(`/api/jobs/${job.id}`)).json()).spec.inputs).toEqual([]);

	// Renaming the second input clears the error, and a third input of another type saves with the rest
	await spec.getByRole('textbox', { name: 'Input 2 name', exact: true }).fill('count');
	await expect(duplicate).toBeHidden();
	await spec.getByRole('button', { name: 'Add input' }).click();
	await spec.getByRole('textbox', { name: 'Input 3 name', exact: true }).fill('dryRun');
	await spec.getByRole('button', { name: 'Input 3 type', exact: true }).click();
	await page.getByRole('option', { name: 'boolean', exact: true }).click();
	await saveForm(spec);

	// The blank criterion and the empty descriptions are dropped rather than stored
	const saved = await (await page.request.get(`/api/jobs/${job.id}`)).json();
	expect(saved.spec.inputs).toEqual([
		{ name: 'repo', type: 'string', description: 'Repository to deploy' },
		{ name: 'count', type: 'integer' },
		{ name: 'dryRun', type: 'boolean' }
	]);
	expect(saved.spec.successCriteria).toEqual(['It responds']);
});

test("A job's declared inputs shape the Run now dialog, which refuses invalid JSON and passes the input and extra instructions to the run", async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Deploy preview', {
		spec: {
			inputs: [
				{ name: 'repo', type: 'string', description: 'Repository to deploy' },
				{ name: 'count', type: 'integer' },
				{ name: 'dryRun', type: 'boolean' }
			]
		}
	});

	// The jobs list opens the Run now dialog from the row's menu
	await page.goto('/jobs');
	await page
		.getByRole('table', { name: 'Jobs' })
		.getByRole('button', { name: 'Actions for Deploy preview' })
		.click();
	await page.getByRole('menuitem', { name: 'Run now' }).click();
	const dialog = page.getByRole('dialog', { name: 'Run now' });
	await expect(dialog).toContainText('“Deploy preview”');

	// A job with inputs shows the input editor right away, prefilled with a value of each declared type, and doesn't mark it optional
	const editor = dialog.getByRole('textbox', { name: 'Run input as JSON' });
	await expect(editor).toContainText('"repo": ""');
	await expect(editor).toContainText('"count": 0');
	await expect(editor).toContainText('"dryRun": false');
	await expect(dialog.getByRole('button', { name: 'Add input JSON' })).toHaveCount(0);
	await expect(dialog.getByText('(optional)')).toHaveCount(1);

	// Broken JSON is refused in the dialog without starting a run
	await replaceCode(editor, '{ "repo": ');
	await dialog.getByRole('button', { name: 'Run now' }).click();
	await expect(dialog.getByRole('alert')).toContainText('Not valid JSON:');
	expect(await countRuns(page.request, job.id)).toBe(0);

	// Valid JSON and extra instructions start the run and open it
	await runUtil.scriptModel(
		page.request,
		runUtil.bashThenFinish('cat /ump/input.json', { status: 'success', summary: 'Read the input' })
	);
	await dialog
		.getByRole('textbox', { name: 'Extra instructions' })
		.fill('  Only deploy the **main** branch  ');
	await replaceCode(editor, '{"repo": "acme/api", "count": 3, "dryRun": true}');
	await dialog.getByRole('button', { name: 'Run now' }).click();
	await expect(page.getByText('Started a run of "Deploy preview"')).toBeVisible();
	await expect(page).toHaveURL(/\/runs\/[0-9a-f-]+$/);
	await expect(pageHeaderMeta(page)).toContainText('Triggered by you');

	// The sandbox reads the input from /ump/input.json, and the run keeps the input and the trimmed instructions
	const runId = page.url().split('/').pop()!;
	expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
	await expect(page.getByRole('region', { name: 'Output of bash' })).toContainText(
		'{"repo":"acme/api","count":3,"dryRun":true}'
	);
	const run = await (await page.request.get(`/api/runs/${runId}`)).json();
	expect(run.input).toEqual({ repo: 'acme/api', count: 3, dryRun: true });
	expect(run.instructions).toBe('Only deploy the **main** branch');
	expect(run.trigger).toBe('manual');
});

test('The overview reports performance tiles and a per-run chart for the chosen period, keeping the period in the URL', async ({
	page
}) => {
	// Tokens make the averages exact, since the fake model has no price and every cost would read $0.00
	await setUsageUnit(page.request, 'tokens');
	const job = await runUtil.createJob(page.request, 'Perf job');

	// Two successful runs and a failed one, each of 1,200 + 300 + 1,400 + 120 = 3,020 tokens
	const runIds: string[] = [];
	for (const outcome of ['success', 'success', 'failure'] as const) {
		const { runId } = await runUtil.runScripted(
			page.request,
			job.id,
			runUtil.bashThenFinish(outcome === 'success' ? 'echo ok' : 'exit 1', {
				status: outcome,
				summary: outcome === 'success' ? 'Reported' : 'Broke'
			})
		);
		runIds.push(runId);
	}

	// The period starts at 30 days, and the tiles count every run of it
	await page.goto(`/jobs/${job.id}`);
	const period = page.getByRole('tablist', { name: 'Period' });
	await expect(period.getByRole('tab', { name: '30d' })).toHaveAttribute('aria-selected', 'true');
	const runsTile = tile(page, 'Runs');
	await expect(runsTile.getByText('3', { exact: true })).toBeVisible();
	await expect(runsTile.getByText('2 succeeded', { exact: true })).toBeVisible();
	await expect(runsTile.getByText(/\b1 failed$/)).toBeVisible();
	const rateTile = tile(page, 'Success rate');
	await expect(rateTile.getByText('67%', { exact: true })).toBeVisible();
	await expect(rateTile.getByText('of 3 finished runs', { exact: true })).toBeVisible();
	await expect(tile(page, 'Average tokens').getByText('3k', { exact: true })).toBeVisible();
	const durationTile = tile(page, 'Median duration');
	await expect(durationTile.getByText(/^Longest \d/)).toBeVisible();
	await expect(durationTile.getByText('—', { exact: true })).toHaveCount(0);

	// The chart speaks of tokens and lists the runs oldest first, with their outcome, mode and usage
	const chart = page.getByRole('img', { name: 'Tokens and duration per run, colored by mode' });
	const rows = chartRows(page);
	await expect(rows).toHaveCount(3);
	await expect(rows.nth(0).getByRole('cell')).toHaveText([
		'#1',
		'Succeeded',
		'Explore',
		'3k',
		/^\d/,
		'0'
	]);
	await expect(rows.nth(2).getByRole('cell')).toHaveText([
		'#3',
		'Failed',
		'Explore',
		'3k',
		/^\d/,
		'0'
	]);

	// Hovering the third run's band, found through its label on the x axis, shows its details, and clicking it opens the run
	const box = (await chart.boundingBox())!;
	const label = (await chart.locator('text', { hasText: /^#3$/ }).boundingBox())!;
	const thirdBand = { x: label.x + label.width / 2 - box.x, y: box.height / 2 };
	await chart.hover({ position: thirdBand });
	await expect(page.getByText('Run #3', { exact: true })).toBeVisible();
	await expect(page.getByText('No playbook', { exact: true })).toBeVisible();
	await chart.click({ position: thirdBand });
	await expect(page).toHaveURL(`/runs/${runIds[2]}`);
	await page.goBack();

	// Another period loads that period's stats, goes into the URL and survives a reload
	const refetch = page.waitForResponse(
		(r) =>
			new URL(r.url()).pathname === `/api/jobs/${job.id}/stats` &&
			new URL(r.url()).searchParams.get('range') === '7d'
	);
	await period.getByRole('tab', { name: '7d' }).click();
	expect((await refetch).ok()).toBeTruthy();
	await expect(page).toHaveURL(/[?&]range=7d/);
	await expect(period.getByRole('tab', { name: '7d' })).toHaveAttribute('aria-selected', 'true');
	await page.reload();
	await expect(period.getByRole('tab', { name: '7d' })).toHaveAttribute('aria-selected', 'true');

	// The default period leaves the URL clean, and a period the page doesn't offer falls back to it
	await period.getByRole('tab', { name: '30d' }).click();
	await expect(page).not.toHaveURL(/range=/);
	await expect(period.getByRole('tab', { name: '30d' })).toHaveAttribute('aria-selected', 'true');
	await page.goto(`/jobs/${job.id}?range=24h`);
	await expect(period.getByRole('tab', { name: '30d' })).toHaveAttribute('aria-selected', 'true');
	await expect(runsTile.getByText('3', { exact: true })).toBeVisible();

	// The API aggregates the same numbers and refuses a period it doesn't know
	const stats = await getStats(page.request, job.id, '7d');
	expect(stats).toMatchObject({ runCount: 3, succeeded: 2, failed: 1, avgTokens: 3020 });
	expect(stats.successRate).toBeCloseTo(2 / 3);
	expect(stats.runs.map((r) => [r.number, r.status, r.mode])).toEqual([
		[1, 'succeeded', 'explore'],
		[2, 'succeeded', 'explore'],
		[3, 'failed', 'explore']
	]);
	const unknown = await page.request.get(`/api/jobs/${job.id}/stats`, {
		params: { range: '24h' }
	});
	expect(unknown.status()).toBe(400);
});

test('A job that never ran shows its schedule, facts and an empty performance state with a Run now shortcut', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Tokyo report', {
		instruction: 'Post the **weekly** summary.',
		graduate: false,
		cron: '0 9 * * 1',
		timezone: 'Asia/Tokyo'
	});
	const graduationOff = 'Graduation is off, so every run uses the agent';

	// The backend arms the schedule in the job's zone, where Monday 09:00 in Tokyo is Monday 00:00 UTC
	const next = new Date(job.nextRunAt!);
	expect([next.getUTCDay(), next.getUTCHours(), next.getUTCMinutes()]).toEqual([1, 0, 0]);
	expect(job.nextRunAt!).toBeGreaterThan(Date.now());

	// The jobs list states the schedule in the job's zone and that it never leaves the agent
	await page.goto('/jobs');
	const row = page.getByRole('table', { name: 'Jobs' }).getByRole('row', { name: /Tokyo report/ });
	await expect(row).toContainText('Mondays at 09:00 · Tokyo');
	await expect(row).toContainText('Never run');
	await expect(row).toContainText('Explore');
	await expect(row).toContainText(graduationOff);

	// The header lists the schedule, the next run, the missing last run, the mode with its note that graduation is off, and that learning is off
	await row.getByRole('link', { name: 'Tokyo report' }).click();
	await expect(page).toHaveURL(`/jobs/${job.id}`);
	await expect(page.getByRole('heading', { level: 1, name: 'Tokyo report' })).toBeVisible();
	const facts = pageHeaderMeta(page);
	await expect(facts).toContainText('Mondays at 09:00 · Tokyo');
	await expect(facts.getByText(/^Next run \S/)).toBeVisible();
	await expect(facts.locator('time')).toHaveAttribute('datetime', next.toISOString());
	await expect(facts.getByText('Never run', { exact: true })).toBeVisible();
	await expect(facts).toContainText('Runs as Explore');
	await expect(facts).toContainText(graduationOff);

	// Without runs there is nothing to measure, so the section points at the first scheduled run instead of empty tiles
	const performance = performanceSection(page);
	await expect(performance.getByText('No runs yet')).toBeVisible();
	await expect(performance).toContainText('Its first scheduled run is');
	await expect(page.getByRole('tablist', { name: 'Period' })).toHaveCount(0);
	await expect(tile(page, 'Runs')).toHaveCount(0);

	// The empty state's Run now opens the dialog, and cancelling it starts nothing
	await performance.getByRole('button', { name: 'Run now' }).click();
	const dialog = page.getByRole('dialog', { name: 'Run now' });
	await expect(dialog).toContainText('“Tokyo report”');

	// A job without inputs keeps the editor behind a button, and the revealed editor is optional and takes the focus
	await expect(dialog.getByRole('textbox', { name: 'Run input as JSON' })).toHaveCount(0);
	await dialog.getByRole('button', { name: 'Add input JSON' }).click();
	const editor = dialog.getByRole('textbox', { name: 'Run input as JSON' });
	await expect(editor).toBeFocused();
	await expect(dialog.getByText('(optional)')).toHaveCount(2);
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toBeHidden();
	expect(await countRuns(page.request, job.id)).toBe(0);

	// Learning off links to the settings, where it can be turned on
	await facts.getByRole('link', { name: 'Learning off' }).click();
	await expect(page).toHaveURL(`/jobs/${job.id}/settings`);
});

test('The job page swaps Never run and the empty performance state for the new run live when a run starts elsewhere, and counts it in its tiles and chart', async ({
	page
}) => {
	const job = await runUtil.createJob(page.request, 'Watched job');
	await gotoListening(page, `/jobs/${job.id}`);
	const facts = pageHeaderMeta(page);
	const performance = performanceSection(page);
	await expect(facts.getByText('Never run', { exact: true })).toBeVisible();
	await expect(performance).toContainText('This job runs on demand.');

	// A run started through the API reaches the open page as live events, without a reload
	await runUtil.scriptModel(page.request, [runUtil.finish('Done')]);
	const runId = await runUtil.startRun(page.request, job.id);
	const lastRun = facts.getByRole('link', { name: /Last run succeeded/ });
	await expect(lastRun).toBeVisible({ timeout: 30_000 });
	await expect(lastRun).toHaveAttribute('href', `/runs/${runId}`);
	await expect(facts.getByText('Never run', { exact: true })).toHaveCount(0);
	await expect(performance.getByText('No runs yet')).toHaveCount(0);
	await expect(page.getByRole('tablist', { name: 'Period' })).toBeVisible();

	// The tiles and the chart count the new run as well, rather than claiming the period has none
	await expect(tile(page, 'Runs').getByText('1', { exact: true })).toBeVisible();
	await expect(card(page, 'Graduation')).not.toContainText('No runs in this period');
	await expect(chartRows(page)).toHaveCount(1);
});
