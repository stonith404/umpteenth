import { expect, test, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';

// These specs need several workspaces, so they only run against a backend with workspaces turned on
test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');
});

function switcher(page: Page) {
	return page.getByRole('button', { name: /switch workspaces/ });
}

async function createInviteLink(page: Page, role: 'Member' | 'Admin' = 'Member') {
	await page.goto('/settings/members');
	await page.getByRole('button', { name: 'Invite member' }).click();
	const dialog = page.getByRole('dialog', { name: 'Invite to workspace' });
	await dialog.getByRole('tab', { name: 'Invite link' }).click();
	if (role !== 'Member') {
		await dialog.getByRole('button', { name: 'Role', exact: true }).click();
		await page.getByRole('option', { name: role }).click();
	}
	await dialog.getByRole('button', { name: 'Create link' }).click();
	const linkDialog = page.getByRole('dialog', { name: 'Invite link created' });
	const url = await linkDialog.getByRole('textbox', { name: 'Invite link' }).inputValue();
	await linkDialog.getByRole('button', { name: 'Done' }).click();
	return new URL(url).pathname;
}

test('Create a workspace and switch between workspaces', async ({ page }) => {
	await page.goto('/');
	await expect(switcher(page)).toContainText('Default');
	await expect(switcher(page)).toContainText('Owner');

	// Creating a workspace moves into it
	await switcher(page).click();
	await page.getByRole('menuitem', { name: 'Create workspace' }).click();
	const dialog = page.getByRole('dialog', { name: 'Create workspace' });
	await dialog.getByLabel('Name').fill('Platform team');
	await dialog.getByRole('button', { name: 'Create workspace' }).click();
	await expect(page.getByText('Created Platform team')).toBeVisible();
	await expect(switcher(page)).toContainText('Platform team');

	// Its jobs are its own, so a job made here doesn't show up in the other workspace
	const created = await page.request.post('/api/jobs', {
		data: { name: 'Platform job', instruction: 'Say hello' }
	});
	expect(created.ok()).toBeTruthy();

	// Switching back lands on the dashboard of the other workspace
	await page.goto('/jobs');
	await switcher(page).click();
	await page.getByRole('menuitem', { name: 'Default' }).click();
	await expect(page).toHaveURL('/');
	await expect(switcher(page)).toContainText('Default');
	await page.goto('/jobs');
	await expect(page.getByText('Platform job')).toBeHidden();
});

test('Someone joins through an invite link, which only works once', async ({
	page,
	browser
}, testInfo) => {
	const invitePath = await createInviteLink(page);
	await expect(page.getByRole('table', { name: 'Pending invites' })).toContainText('Invite link');

	// Bob opens the link, sees what he is invited to, and joins
	const bob = await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder'
	});
	await bob.goto(invitePath);
	await expect(bob.getByRole('heading', { name: 'Join Default' })).toBeVisible();
	await expect(bob.getByText('E2E User invited you to join as member.')).toBeVisible();
	await bob.getByRole('button', { name: 'Join workspace' }).click();
	await expect(bob).toHaveURL('/');
	await expect(switcher(bob)).toContainText('Default');
	await expect(switcher(bob)).toContainText('Member');

	// The link is used up now
	const carol = await authUtil.pageAs(browser, testInfo, {
		subject: 'carol',
		email: 'carol@example.com',
		name: 'Carol'
	});
	await carol.goto(invitePath);
	await expect(carol.getByText('This invite no longer works')).toBeVisible();

	// The owner sees Bob among the members, and the invite is gone
	await page.reload();
	const members = page.getByRole('table', { name: 'Members' });
	await expect(members.getByRole('row', { name: /Bob Builder/ })).toBeVisible();
	await expect(page.getByRole('table', { name: 'Pending invites' })).toBeHidden();
});

test('A sign-in from an invite link joins right away without a personal workspace', async ({
	page
}) => {
	const invitePath = await createInviteLink(page, 'Admin');

	const login = await authUtil.signInAs(page, {
		subject: 'dana',
		email: 'dana@example.com',
		name: 'Dana',
		redirect: invitePath
	});
	expect(login.redirect).toBe('/');
	const workspaces = await (await page.request.get('/api/workspaces')).json();
	expect(workspaces).toEqual([expect.objectContaining({ name: 'Default', role: 'admin' })]);
});

test('An email invite waits for a sign-in with the verified address', async ({
	page,
	browser
}, testInfo) => {
	await page.goto('/settings/members');
	await page.getByRole('button', { name: 'Invite member' }).click();
	const dialog = page.getByRole('dialog', { name: 'Invite to workspace' });
	await dialog.getByLabel('Email').fill('carol@example.com');
	await dialog.getByRole('button', { name: 'Role', exact: true }).click();
	await page.getByRole('option', { name: 'Admin' }).click();
	await dialog.getByRole('button', { name: 'Invite' }).click();
	await expect(page.getByText('carol@example.com joins the next time they sign in')).toBeVisible();
	await expect(page.getByRole('table', { name: 'Pending invites' })).toContainText(
		'carol@example.com'
	);

	// An account whose provider didn't vouch for the address gets a workspace of its own instead
	const impostor = await authUtil.pageAs(browser, testInfo, {
		subject: 'impostor',
		email: 'carol@example.com',
		emailVerified: false,
		name: 'Impostor'
	});
	await impostor.goto('/');
	await expect(switcher(impostor)).toContainText("Impostor's workspace");

	// Carol signs in with the verified address and lands in the workspace as an admin
	const carol = await authUtil.pageAs(browser, testInfo, {
		subject: 'carol',
		email: 'Carol@Example.com',
		name: 'Carol Danvers'
	});
	await carol.goto('/');
	await expect(switcher(carol)).toContainText('Default');
	await expect(switcher(carol)).toContainText('Admin');
});

test('Members can look at the settings but only admins change them', async ({
	page,
	browser
}, testInfo) => {
	const invitePath = await createInviteLink(page);
	const bob = await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder',
		redirect: invitePath
	});

	// Bob, a member, reads the providers but has nothing to change them with
	await bob.goto('/settings/providers');
	await expect(
		bob.getByText('Only admins of the workspace can change its providers and models.')
	).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Add provider' })).toBeHidden();
	const refused = await bob.request.patch('/api/settings', { data: { retentionDays: 5 } });
	expect(refused.status()).toBe(403);

	// Made an admin, he can
	await page.goto('/settings/members');
	await page.getByRole('button', { name: 'Role of Bob Builder', exact: true }).click();
	await page.getByRole('option', { name: 'Admin' }).click();
	await expect(page.getByText('Bob Builder is now admin')).toBeVisible();
	await bob.reload();
	await expect(bob.getByRole('button', { name: 'Add provider' })).toBeVisible();
});

test('Only admins delete runs', async ({ page, browser }, testInfo) => {
	const job = await runUtil.createJob(page.request, 'Shared job');
	const { runId } = await runUtil.runScripted(page.request, job.id, [runUtil.finish('Done')]);
	const invitePath = await createInviteLink(page);
	const bob = await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder',
		redirect: invitePath
	});

	// Bob, a member, sees the run without a way to delete it, and the API refuses him
	await bob.goto('/runs');
	const table = bob.getByRole('table', { name: 'Runs' });
	await expect(table.getByRole('row', { name: /Shared job/ })).toBeVisible();
	await expect(table.getByRole('checkbox')).toHaveCount(0);
	await expect(table.getByRole('button', { name: /Actions for/ })).toHaveCount(0);
	await bob.goto(`/runs/${runId}`);
	await expect(bob.getByRole('button', { name: 'Retry' })).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Delete', exact: true })).toBeHidden();
	expect((await bob.request.delete(`/api/runs/${runId}`)).status()).toBe(403);
	expect((await bob.request.post('/api/runs/delete', { data: { ids: [runId] } })).status()).toBe(
		403
	);
});

test('The owner hands over the workspace and can leave it then', async ({
	page,
	browser
}, testInfo) => {
	const invitePath = await createInviteLink(page);
	await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder',
		redirect: invitePath
	});

	// Only a handover makes someone else the owner
	await page.reload();
	await page.getByRole('button', { name: 'Actions for Bob Builder' }).click();
	await page.getByRole('menuitem', { name: 'Make owner' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Hand over ownership' }).click();
	await expect(page.getByText('Bob Builder now owns the workspace')).toBeVisible();
	await expect(switcher(page)).toContainText('Admin');

	// Leaving the only workspace lands in a new personal one
	await page.goto('/settings/general');
	await page.getByRole('button', { name: 'Leave workspace' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Leave workspace' }).click();
	await expect(page).toHaveURL('/');
	await expect(switcher(page)).toContainText("E2E User's workspace");
	await expect(switcher(page)).toContainText('Owner');
});

test('The owner deletes a workspace with everything in it', async ({ page }) => {
	const created = await page.request.post('/api/workspaces', { data: { name: 'Doomed' } });
	expect(created.ok()).toBeTruthy();
	await page.goto('/settings/general');
	await expect(switcher(page)).toContainText('Doomed');

	await page.getByRole('button', { name: 'Delete workspace' }).click();
	await page.getByRole('alertdialog').getByRole('textbox').fill('Doomed');
	await page.getByRole('alertdialog').getByRole('button', { name: 'Delete workspace' }).click();
	await expect(page.getByText('Deleted Doomed')).toBeVisible();
	await expect(page).toHaveURL('/');
	await expect(switcher(page)).toContainText('Default');

	await switcher(page).click();
	await expect(page.getByRole('menuitem', { name: 'Doomed' })).toBeHidden();
});
