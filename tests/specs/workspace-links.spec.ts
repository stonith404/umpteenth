import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// Links from notifications name the run's workspace, which only matters with workspaces turned on
test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');
});

test('A notification link opened without a session leads to the run after signing in', async ({
	page,
	browser
}, testInfo) => {
	// The run lives in the default workspace, and creating a second workspace makes that one the user's last
	const me = (await (await page.request.get('/api/users/me')).json()) as {
		workspace: { id: string };
	};
	const job = await runUtil.createJob(page.request, 'Linked job');
	const runId = await runUtil.startRun(page.request, job.id);
	const created = await page.request.post('/api/workspaces', { data: { name: 'Platform team' } });
	expect(created.ok()).toBeTruthy();
	const link = `/runs/${runId}?workspace=${me.workspace.id}`;

	// A browser without a session, such as another device or one whose session expired, opens the link and has to sign in first
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const guest = await context.newPage();
	await guest.goto(link);
	await expect(guest).toHaveURL(/\/login\?redirect=/);
	const returnTo = new URL(guest.url()).searchParams.get('redirect');
	expect(returnTo).toBe(link);

	// Signing in returns to the link, which moves the session into the run's workspace
	const { redirect } = await authUtil.signInAs(guest, { redirect: returnTo ?? '/' });
	await guest.goto(redirect);
	await expect(guest).toHaveURL(`/runs/${runId}`);
	await expect(guest.getByRole('heading', { level: 1 })).toContainText('Linked job');
	await context.close();
});
