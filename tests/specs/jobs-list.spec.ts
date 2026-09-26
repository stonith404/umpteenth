import { expect, test, type Locator, type Page } from '@playwright/test';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { runStatus } from '../utils/run-view.util';
import runUtil from '../utils/run.util';

// Some of these specs start runs in real sandboxes, so they get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The jobs list's table
function jobsTable(page: Page) {
	return page.getByRole('table', { name: 'Jobs' });
}

// The loaded body rows of a table, without the header row and the loading skeleton
function bodyRows(table: Locator) {
	return table.locator('tbody tr[data-row-id]');
}

// The job name links of the jobs list, one per row in the order the rows show
function jobNames(table: Locator) {
	return bodyRows(table).locator('td:first-child').getByRole('link');
}

// The row of one job in the jobs list, found by the job's name anywhere in the row's name
function jobRow(table: Locator, name: string) {
	const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	return table.getByRole('row', { name: new RegExp(escaped) });
}

// The strip of a job's latest runs in its row, whose links are named like "#3 · Skipped · Sep 29, 10:00"
function recentRuns(row: Locator) {
	return row.getByRole('list', { name: 'Recent runs' });
}

test('The jobs list shows the latest ten runs of a job as a strip that updates live and opens the runs', async ({
	page
}) => {
	// A job that never ran says so and has no blocks in its strip
	const job = await runUtil.createJob(page.request, 'Strip job');
	await page.goto('/jobs');
	const table = jobsTable(page);
	const row = jobRow(table, 'Strip job');
	const strip = recentRuns(row);
	await expect(row).toContainText('Never run');
	await expect(strip.getByRole('link')).toHaveCount(0);

	// A busy run keeps the job active, so every further trigger is skipped under the default policy without a sandbox of its own
	await runUtil.startBusyRun(page.request, job.id);

	// The first run shows up in the strip without a reload, and its block then turns to Running in place as the sandbox starts
	await expect(strip.getByRole('link', { name: /^#1 · Running · / })).toBeVisible({
		timeout: 30_000
	});
	await expect(row).not.toContainText('Never run');

	// Eleven more triggers make twelve runs, two more than the strip has room for
	let newestRunId = '';
	for (let i = 0; i < 11; i++) {
		const { runId, status } = await runUtil.triggerRun(page.request, job.id);
		expect(status).toBe('skipped');
		newestRunId = runId;
	}

	// The strip keeps the ten newest runs with the oldest on the left, so the first two runs drop off
	const links = strip.getByRole('link');
	await expect(links).toHaveCount(10);
	await expect(links.first()).toHaveAccessibleName(/^#3 · Skipped · /);
	await expect(links.last()).toHaveAccessibleName(/^#12 · Skipped · /);
	await expect(strip.getByRole('link', { name: /^#1 · / })).toHaveCount(0);
	await expect(strip.getByRole('link', { name: /^#2 · / })).toHaveCount(0);

	// The API returns the same ten runs newest first, and the newest one as the last run
	const listed = await (await page.request.get('/api/jobs')).json();
	expect(listed.items[0].recentRuns).toHaveLength(10);
	expect(listed.items[0].recentRuns.map((run: { number: number }) => run.number)).toEqual([
		12, 11, 10, 9, 8, 7, 6, 5, 4, 3
	]);
	expect(listed.items[0].lastRun.number).toBe(12);
	expect(listed.items[0].runCount).toBe(12);

	// A block of the strip opens its run rather than the job the row links to
	await links.last().click();
	await expect(page).toHaveURL(`/runs/${newestRunId}`);
	await expect(runStatus(page)).toHaveText('Skipped');

	// Anywhere else in the row, such as its schedule line, opens the job
	await page.goBack();
	await expect(page).toHaveURL('/jobs');
	await row.getByText('On demand', { exact: true }).first().click();
	await expect(page).toHaveURL(`/jobs/${job.id}`);
});

test("The job's Runs tab lists only that job's runs, filters them by trigger and offers new runs while sorted", async ({
	page
}) => {
	const tabJob = await runUtil.createJob(page.request, 'Tab job');
	const otherJob = await runUtil.createJob(page.request, 'Other job');

	// One manual run and one run the schedule started, one after the other so they never share the fake model's script
	await runUtil.runScripted(page.request, tabJob.id, [runUtil.finish('Manual run')]);
	await runUtil.scriptModel(page.request, [runUtil.finish('Scheduled run')]);
	await runUtil.fireSchedule(page.request, tabJob.id);
	const scheduled = await runUtil.listRuns(page.request, { job: tabJob.id, trigger: 'schedule' });
	expect(scheduled.items.length).toBeGreaterThan(0);
	await runUtil.waitForRun(page.request, scheduled.items[0].id);

	// The Runs tab shows both runs by number with what triggered them
	await page.goto(`/jobs/${tabJob.id}`);
	await page.getByRole('tab', { name: 'Runs' }).click();
	await expect(page).toHaveURL(`/jobs/${tabJob.id}/runs`);
	const table = page.getByRole('table', { name: 'Runs of Tab job' });
	const rows = bodyRows(table);
	const manualRow = table
		.getByRole('row')
		.filter({ has: page.getByRole('link', { name: '#1', exact: true }) });
	const scheduledRow = table
		.getByRole('row')
		.filter({ has: page.getByRole('link', { name: '#2', exact: true }) });
	await expect(rows).toHaveCount(2);
	await expect(manualRow).toContainText('Manual');
	await expect(scheduledRow).toContainText('Schedule');

	// The trigger filter keeps only the scheduled run, also after a reload
	await page.getByRole('button', { name: 'Trigger' }).click();
	await page.getByRole('option', { name: 'Schedule' }).click();
	await page.keyboard.press('Escape');
	await expect(page).toHaveURL(/trigger=schedule/);
	await expect(scheduledRow).toBeVisible();
	await expect(manualRow).toBeHidden();
	await page.reload();
	await expect(scheduledRow).toBeVisible();
	await expect(rows).toHaveCount(1);

	// Reset brings the manual run back
	await page.getByRole('button', { name: 'Reset' }).click();
	await expect(page).not.toHaveURL(/trigger=/);
	await expect(rows).toHaveCount(2);

	// A sorted view doesn't reorder itself when runs arrive
	await table.getByRole('button', { name: 'Duration' }).click();
	await expect(page).toHaveURL(/sort=-?duration/);

	// A run of another job raises nothing here
	await runUtil.runScripted(page.request, otherJob.id, [runUtil.finish('Other run')]);
	const newRuns = page.getByRole('button', { name: /new runs?$/ });
	await expect(newRuns).toHaveCount(0);

	// A run of this job waits behind a button that counts only it
	await runUtil.runScripted(page.request, tabJob.id, [runUtil.finish('Third run')]);
	await expect(newRuns).toHaveText('1 new run');
	await expect(rows).toHaveCount(2);

	// The button goes back to the default sort, which shows the new run and still none of the other job's
	await newRuns.click();
	await expect(page).not.toHaveURL(/sort=/);
	await expect(table.getByRole('link', { name: '#3', exact: true })).toBeVisible();
	await expect(rows).toHaveCount(3);
	await expect(newRuns).toHaveCount(0);

	// The API agrees on the job's runs and what triggered each of them
	const { items, total } = await runUtil.listRuns(page.request, {
		job: tabJob.id,
		sort: 'queuedAt'
	});
	expect(total).toBe(3);
	expect(items.map((run) => run.trigger)).toEqual(['manual', 'schedule', 'manual']);
});

test('The jobs list has no search while empty, then searches names and instructions on the server, case-insensitively and with literal wildcards', async ({
	page
}) => {
	// An empty workspace offers one way to create a job and nothing to search
	await page.goto('/jobs');
	await expect(page.getByText('No jobs yet')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Create job' })).toHaveCount(1);
	const search = page.getByRole('searchbox', { name: 'Search jobs' });
	await expect(search).toHaveCount(0);

	// With jobs the header takes over the create link
	await runUtil.createJob(page.request, 'Alpha report', {
		instruction: 'Summarize the zebra migration.'
	});
	await runUtil.createJob(page.request, 'Beta digest', { instruction: 'Count stale PRs.' });
	await runUtil.createJob(page.request, '100% uptime check', { instruction: 'Ping the API.' });
	await page.reload();
	const table = jobsTable(page);
	const rows = bodyRows(table);
	const alpha = jobRow(table, 'Alpha report');
	const beta = jobRow(table, 'Beta digest');
	const uptime = jobRow(table, '100% uptime check');
	await expect(rows).toHaveCount(3);
	await expect(page.getByText('No jobs yet')).toBeHidden();
	await expect(page.getByRole('link', { name: 'Create job' })).toHaveCount(1);

	// An upper-case word that only the instruction contains finds the job, and the search lives in the URL
	await search.fill('ZEBRA');
	await expect(page).toHaveURL(/search=ZEBRA/);
	await expect(alpha).toBeVisible();
	await expect(rows).toHaveCount(1);
	await page.reload();
	await expect(search).toHaveValue('ZEBRA');
	await expect(alpha).toBeVisible();
	await expect(rows).toHaveCount(1);

	// A percent sign matches itself instead of anything
	await search.fill('%');
	await expect(page).toHaveURL(/search=%25/);
	await expect(uptime).toBeVisible();
	await expect(rows).toHaveCount(1);

	// An underscore matches no single character either, so nothing matches and the header still offers to create a job
	await search.fill('_');
	await expect(page.getByText('No results match your search or filters')).toBeVisible();
	await expect(rows).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Create job' })).toBeVisible();

	// Clearing the filters brings every job back
	await page.getByRole('button', { name: 'Clear filters' }).click();
	await expect(search).toHaveValue('');
	await expect(page).not.toHaveURL(/search=/);
	await expect(rows).toHaveCount(3);

	// Escape in the search field clears it too
	await search.fill('beta');
	await expect(beta).toBeVisible();
	await expect(rows).toHaveCount(1);
	await search.press('Escape');
	await expect(search).toHaveValue('');
	await expect(page).not.toHaveURL(/search=/);
	await expect(rows).toHaveCount(3);
});

test('The jobs list sorts by name and by next run with unscheduled jobs last, and pages with the view in the URL', async ({
	page
}) => {
	// One letter case only, since the database's collation decides how upper and lower case names sort
	const onDemand = Array.from({ length: 26 }, (_, i) => `Job ${String(i + 1).padStart(2, '0')}`);
	for (const name of onDemand) {
		await runUtil.createJob(page.request, name);
	}

	// Schedules far apart and rarely due, whose order by next run comes from the server rather than today's date
	const monthly = await runUtil.createJob(page.request, 'Sched monthly', {
		cron: '0 9 1 * *',
		timezone: 'UTC'
	});
	const yearly = await runUtil.createJob(page.request, 'Sched yearly', {
		cron: '0 10 1 7 *',
		timezone: 'UTC'
	});
	const scheduled = [monthly, yearly]
		.sort((a, b) => a.nextRunAt! - b.nextRunAt!)
		.map((job) => job.name);

	// The default view sorts by name and shows the first 25 jobs
	await page.goto('/jobs');
	const table = jobsTable(page);
	const names = jobNames(table);
	const jobHeader = table.getByRole('columnheader', { name: 'Job', exact: true });
	const nextRunHeader = table.getByRole('columnheader', { name: 'Next run', exact: true });
	await expect(jobHeader).toHaveAttribute('aria-sort', 'ascending');
	await expect(names).toHaveText(onDemand.slice(0, 25));
	await expect(page.getByText(/Showing 1–25 of\s*28/)).toBeVisible();

	// The second page holds the rest and survives a reload
	await page.getByRole('button', { name: 'Next page' }).click();
	await expect(page).toHaveURL(/page=2/);
	await expect(names).toHaveText(['Job 26', 'Sched monthly', 'Sched yearly']);
	await expect(page.getByText(/Showing 26–28 of\s*28/)).toBeVisible();
	await page.reload();
	await expect(names).toHaveText(['Job 26', 'Sched monthly', 'Sched yearly']);

	// Sorting by name the other way starts over on the first page
	await table.getByRole('button', { name: 'Job', exact: true }).click();
	await expect(page).toHaveURL(/sort=-name/);
	await expect(page).not.toHaveURL(/page=/);
	await expect(jobHeader).toHaveAttribute('aria-sort', 'descending');
	await expect(names.first()).toHaveText('Sched yearly');
	await expect(names.nth(1)).toHaveText('Sched monthly');
	await expect(names.nth(2)).toHaveText('Job 26');

	// Back to ascending is the default sort, which the URL leaves out
	await table.getByRole('button', { name: 'Job', exact: true }).click();
	await expect(page).not.toHaveURL(/sort=/);
	await expect(jobHeader).toHaveAttribute('aria-sort', 'ascending');
	await expect(names.first()).toHaveText('Job 01');

	// The first click on the next run puts the soonest run on top and the jobs without a schedule last
	await table.getByRole('button', { name: 'Next run', exact: true }).click();
	await expect(page).toHaveURL(/sort=nextRunAt(&|$)/);
	await expect(nextRunHeader).toHaveAttribute('aria-sort', 'ascending');
	await expect(names.first()).toHaveText(scheduled[0]);
	await expect(names.nth(1)).toHaveText(scheduled[1]);
	await expect(names.nth(2)).toHaveText(/^Job \d\d$/);

	// Descending reverses the scheduled jobs and still keeps the unscheduled ones last
	await table.getByRole('button', { name: 'Next run', exact: true }).click();
	await expect(page).toHaveURL(/sort=-nextRunAt/);
	await expect(nextRunHeader).toHaveAttribute('aria-sort', 'descending');
	await expect(names.first()).toHaveText(scheduled[1]);
	await expect(names.nth(1)).toHaveText(scheduled[0]);
	await expect(names.nth(2)).toHaveText(/^Job \d\d$/);

	// A bigger page size fits every job and is kept in the URL
	await page.getByRole('button', { name: 'Rows per page' }).click();
	await page.getByRole('option', { name: '50' }).click();
	await expect(page).toHaveURL(/pageSize=50/);
	await expect(names).toHaveCount(28);
	await expect(page.getByText(/Showing 1–28 of\s*28/)).toBeVisible();
	await expect(page.getByRole('button', { name: 'Next page' })).toBeDisabled();
});

test("A job of another workspace is not found in the UI, and the job's endpoints refuse reads and writes", async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// The owner's job has run once, so an empty runs list for someone else can't pass on its own
	const job = await runUtil.createJob(page.request, 'Private job');
	await runUtil.runScripted(page.request, job.id, [runUtil.finish('Done')]);

	// Bob signs in for the first time and gets a workspace of his own
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	try {
		// The job's pages tell him it doesn't exist rather than showing it
		for (const path of [`/jobs/${job.id}`, `/jobs/${job.id}/runs`]) {
			await bob.goto(path);
			await expect(bob.getByRole('heading', { level: 1, name: 'Job not found' })).toBeVisible();
			await expect(
				bob.getByText('It may have been deleted, or it belongs to another workspace.')
			).toBeVisible();
			await expect(bob.getByText('Private job')).toHaveCount(0);
		}

		// The way out leads to his own, empty jobs list
		await bob.getByRole('link', { name: 'Back to jobs' }).click();
		await expect(bob).toHaveURL('/jobs');
		await expect(bob.getByText('No jobs yet')).toBeVisible();

		// The job's endpoints answer as if the job didn't exist, for reads and writes alike
		// The message tells that apart from a route that went missing, which answers 404 too
		const playbook = {
			content: { learnings: [], toolkit: [], dockerfile: null, setup: null, main: 'echo hi' },
			summary: 'Hijacked',
			baseVersion: 0
		};
		const calls = [
			() => bob.request.get(`/api/jobs/${job.id}`),
			() => bob.request.get(`/api/jobs/${job.id}/stats`),
			() => bob.request.post(`/api/jobs/${job.id}/runs`, { data: {} }),
			() => bob.request.patch(`/api/jobs/${job.id}`, { data: { name: 'Hijacked' } }),
			() => bob.request.post(`/api/jobs/${job.id}/webhook-token`),
			() => bob.request.get(`/api/jobs/${job.id}/playbook`),
			() => bob.request.put(`/api/jobs/${job.id}/playbook`, { data: playbook }),
			() => bob.request.get(`/api/jobs/${job.id}/state`),
			() => bob.request.put(`/api/jobs/${job.id}/state/owner`, { data: { value: 'bob' } }),
			() => bob.request.get(`/api/jobs/${job.id}/secrets`),
			() => bob.request.get(`/api/jobs/${job.id}/mcp-servers`),
			() => bob.request.delete(`/api/jobs/${job.id}`)
		];
		for (const call of calls) {
			const response = await call();
			expect(response.status(), response.url()).toBe(404);
			expect(await response.json(), response.url()).toMatchObject({ message: 'Job not found' });
		}
		expect((await runUtil.listRuns(bob.request, { job: job.id })).total).toBe(0);
	} finally {
		await bob.context().close();
	}

	// The owner still has the job exactly as it was
	const owned = await page.request.get(`/api/jobs/${job.id}`);
	expect(owned.status()).toBe(200);
	expect(await owned.json()).toMatchObject({
		name: 'Private job',
		hasWebhookToken: false,
		playbookVersion: 0
	});
	expect((await runUtil.listRuns(page.request, { job: job.id })).total).toBe(1);
	const state = await (await page.request.get(`/api/jobs/${job.id}/state`)).json();
	expect(state.total).toBe(0);
});
