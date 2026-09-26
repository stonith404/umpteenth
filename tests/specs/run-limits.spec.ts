import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import { runDetail, runHeader, runStatus, timelineStep } from '../utils/run-view.util';
import runUtil from '../utils/run.util';

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Prices the seeded fake model in dollars per 1M tokens, as the providers page shows prices, so runs on it cost money
// The model is free otherwise, and the reset in beforeEach seeds it free again
async function priceFakeModel(request: APIRequestContext, price: { input: number }) {
	const listed = await request.get('/api/models', { params: { search: 'fake-model' } });
	expect(listed.ok()).toBeTruthy();
	const { items } = (await listed.json()) as { items: { id: string }[] };

	// The API takes prices in micro-USD per 1M tokens
	const response = await request.patch(`/api/models/${items[0].id}`, {
		data: {
			price: { in: price.input * 1e6, out: 0, cacheRead: 0, cacheWrite: 0 }
		}
	});
	expect(response.ok()).toBeTruthy();
}

// Fills in fields of one of the workspace's settings cards and saves that card
async function saveWorkspaceSettings(page: Page, card: string, values: Record<string, string>) {
	await page.goto('/settings/general');
	const form = page.getByRole('form', { name: card });
	for (const [label, value] of Object.entries(values)) {
		await form.getByLabel(label, { exact: true }).fill(value);
	}
	await saveForm(form);
}

test("Sandbox defaults bound every run, and a job's own limit wins over them", async ({ page }) => {
	const job = await runUtil.createJob(page.request, 'Limits job');

	// Tighten the workspace defaults every job without limits of its own runs with
	await saveWorkspaceSettings(page, 'Sandbox defaults', {
		'Max turns': '1',
		CPUs: '0.5',
		Memory: '256'
	});

	// The job's limits stay empty and show the workspace defaults they fall back to
	await page.goto(`/jobs/${job.id}/settings`);
	const sandbox = page.getByRole('form', { name: 'Sandbox' });
	await expect(sandbox.getByLabel('Max turns')).toHaveValue('');
	await expect(sandbox.getByLabel('Max turns')).toHaveAttribute('placeholder', '1');
	await expect(sandbox.getByLabel('CPUs')).toHaveAttribute('placeholder', '0.5');
	await expect(sandbox.getByLabel('Memory')).toHaveAttribute('placeholder', '256');

	// The first turn reads the container's cgroup limits, and the second one is never asked for
	// The cgroup v1 files are the fallback for hosts without cgroup v2, where the CPU quota comes without its period
	const readLimits = runUtil.bashThenFinish(
		'echo "cpu=$(cat /sys/fs/cgroup/cpu.max 2>/dev/null || cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)"; echo "memory=$(cat /sys/fs/cgroup/memory.max 2>/dev/null || cat /sys/fs/cgroup/memory/memory.limit_in_bytes)"',
		{ status: 'success', summary: 'Read the limits' }
	);
	const first = await runUtil.runScripted(page.request, job.id, readLimits);
	expect(first.status).toBe('failed');
	expect(await runUtil.pendingAnswers(page.request)).toBe(1);

	// The run page names the turn limit, and the sandbox had the default memory and half a CPU
	await page.goto(`/runs/${first.runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	const alert = runHeader(page).getByRole('alert');
	await expect(alert).toContainText('The run exceeded its limit of 1 turn');
	await expect(alert).not.toContainText('1 turns');
	await expect(runDetail(page, 'Turns')).toHaveText('1');
	const timeline = page.getByTestId('run-timeline');
	const output = timeline.getByRole('region', { name: 'Output of bash' });
	await expect(output).toContainText(/cpu=50000( 100000)?\n/);
	await expect(output).toContainText('memory=268435456');

	// The job raises only its own turn limit
	await page.goto(`/jobs/${job.id}/settings`);
	await sandbox.getByLabel('Max turns').fill('3');
	await saveForm(sandbox);

	// The next run finishes in its second turn, still in a sandbox with the workspace's memory limit
	const second = await runUtil.runScripted(page.request, job.id, readLimits);
	expect(second.status).toBe('succeeded');
	await page.goto(`/runs/${second.runId}`);
	await expect(runStatus(page)).toHaveText('Succeeded');
	await expect(runDetail(page, 'Turns')).toHaveText('2');
	await expect(output).toContainText('memory=268435456');
	await expect(timeline.getByText('Read the limits')).toBeVisible();
});

test('A run stops at its cost limit, and the daily spend limit refuses runs before they start', async ({
	page
}) => {
	// A turn of 1k input tokens at $1,000 per 1M tokens costs $1.00
	await priceFakeModel(page.request, { input: 1000 });
	const job = await runUtil.createJob(page.request, 'Budget job');
	await saveWorkspaceSettings(page, 'Sandbox defaults', { 'Max cost per run': '0.5' });

	// The first turn costs more than a run may, so the run ends before its second turn
	const pricey = await runUtil.runScripted(page.request, job.id, [
		{
			text: 'Checking the prices.',
			toolCalls: [{ name: 'bash', args: { command: 'echo pricey' } }],
			usage: { input: 1000 }
		},
		runUtil.finish('Never reached')
	]);
	expect(pricey.status).toBe('failed');
	expect(await runUtil.pendingAnswers(page.request)).toBe(1);
	const priceyRun = await runUtil.getRun(page.request, pricey.runId);
	expect(priceyRun.cost).toBe(1_000_000);
	expect(priceyRun.turns).toBe(1);

	// The run page names the cost limit next to what the run cost
	await page.goto(`/runs/${pricey.runId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	await expect(runHeader(page).getByRole('alert')).toContainText(
		'The run exceeded its cost limit of $0.50'
	);
	await expect(runDetail(page, 'Cost')).toHaveText('$1.00');
	await expect(runDetail(page, 'Turns')).toHaveText('1');
	const timeline = page.getByTestId('run-timeline');
	await expect(timelineStep(page, 'Turn 1')).toContainText('$1.00');

	// Today's spend already passes the daily limit, so the next run fails without a sandbox or a model call
	await saveWorkspaceSettings(page, 'Spend and retention', { 'Daily spend limit': '0.5' });
	await runUtil.scriptModel(page.request, [runUtil.finish('Back in budget')]);
	const refusedId = await runUtil.startRun(page.request, job.id);
	expect(await runUtil.waitForRun(page.request, refusedId)).toBe('failed');
	expect(await runUtil.pendingAnswers(page.request)).toBe(1);
	const refused = await runUtil.getRun(page.request, refusedId);
	expect(refused).toMatchObject({ turns: 0, cost: 0, imageRef: null });

	// The run page names the daily limit and shows a run that never got going
	await page.goto(`/runs/${refusedId}`);
	await expect(runStatus(page)).toHaveText('Failed');
	await expect(runHeader(page).getByRole('alert')).toContainText(
		'The workspace reached its daily spend limit of $0.50'
	);
	await expect(timeline.getByText('Error', { exact: true })).toBeVisible();
	await expect(timeline.getByText('Sandbox created')).toHaveCount(0);
	await expect(timeline.getByText('Turn 1', { exact: true })).toHaveCount(0);
	await expect(runDetail(page, 'Cost')).toHaveText('—');
	await expect(runDetail(page, 'Turns')).toHaveText('0');

	// A daily limit of 0 means no limit, so runs go through again
	await saveWorkspaceSettings(page, 'Spend and retention', { 'Daily spend limit': '0' });
	const allowedId = await runUtil.startRun(page.request, job.id);
	expect(await runUtil.waitForRun(page.request, allowedId)).toBe('succeeded');
	expect((await runUtil.getRun(page.request, allowedId)).summary).toBe('Back in budget');
});

// Starts a run of a job whose 30 s timeout, the shortest a job may have, passes while the given commands run
async function startSlowRun(page: Page, commands: { command: string; timeout_sec?: number }[]) {
	const job = await runUtil.createJob(page.request, 'Slow job', { limits: { timeoutSeconds: 30 } });
	await runUtil.scriptModel(page.request, [
		...commands.map((args) => ({ toolCalls: [{ name: 'bash', args }] })),
		runUtil.finish('Never reached')
	]);
	const runId = await runUtil.startRun(page.request, job.id);
	await page.goto(`/runs/${runId}`);
	return runId;
}

test("A killed command lets the run go on until the job's timeout ends it as timed out, and it can still be learned from", async ({
	page
}) => {
	// The run can't time out sooner than the 30 s minimum, which leaves too little of the default budget
	test.setTimeout(90_000);
	// The commands print computed numbers, since the step also shows the command line itself inside its output region
	const runId = await startSlowRun(page, [
		{ command: 'echo "started $((1+1))"; sleep 30', timeout_sec: 2 },
		{ command: 'echo "still going $((40+2))"; sleep 120' }
	]);

	// The first command is killed after its own 2 s and keeps what it printed before, and the agent gets to run the next one
	const timeline = page.getByTestId('run-timeline');
	const killed = timelineStep(page, 'sleep 30');
	await expect(killed.getByText('timed out', { exact: true })).toBeVisible({ timeout: 20_000 });
	const killedOutput = killed.getByRole('region', { name: 'Output of bash' });
	await expect(killedOutput).toContainText('started 2');
	await expect(killedOutput).toContainText('the command timed out after 2s and was killed');

	// The second command streams its output while it runs into the deadline
	const slow = timelineStep(page, 'sleep 120');
	await expect(slow.getByRole('region', { name: 'Output of bash' })).toContainText(
		'still going 42',
		{ timeout: 20_000 }
	);
	await expect(slow).toContainText('running');
	await expect(runStatus(page)).toHaveText('Running');

	// The job's timeout ends the whole run while its second command still runs, and both the alert and the command say it was the time limit
	await expect(runStatus(page)).toHaveText('Timed out', { timeout: 45_000 });
	const alert = runHeader(page).getByRole('alert');
	await expect(alert).toContainText('Timed out');
	await expect(alert).toContainText('The run exceeded its time limit');
	const slowOutput = slow.getByRole('region', { name: 'Output of bash' });
	await expect(slowOutput).toContainText(
		'the command was cut off because the run exceeded its time limit'
	);
	await expect(slowOutput).toContainText('still going 42');
	await expect(timeline.locator('[data-slot="timeline-step"]').last()).toContainText('Timed out');
	await expect(slow).not.toContainText('running');
	await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeHidden();
	await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();

	// The run ended at the job's 30 s rather than the workspace's 15 min, and never asked the model for another turn
	const run = (await (await page.request.get(`/api/runs/${runId}`)).json()) as {
		status: string;
		startedAt: number;
		finishedAt: number;
	};
	expect(run.status).toBe('timed_out');
	expect(run.finishedAt - run.startedAt).toBeGreaterThanOrEqual(30_000);
	expect(run.finishedAt - run.startedAt).toBeLessThan(45_000);
	expect(await runUtil.pendingAnswers(page.request)).toBe(1);

	// Unlike a cancelled run, a timed out one can still teach the job something
	await runUtil.scriptModel(page.request, [
		runUtil.reflection('The export needs more time than the job allows')
	]);
	await runHeader(page).getByRole('button', { name: 'Learn from this run' }).click();
	await expect(page.getByText('Learning from this run')).toBeVisible();
	await expect(page.getByText('What this run taught the job')).toBeVisible({ timeout: 20_000 });
	await expect(page.getByText('The export needs more time than the job allows')).toBeVisible();
	expect((await runUtil.waitForReflection(page.request, runId)).reflection).toBe('done');
});
