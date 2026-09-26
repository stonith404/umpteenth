import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';

// With workspaces turned off everyone shares one workspace, which the HA test stack runs with
test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
	test.skip(await authUtil.workspacesEnabled(page), 'workspaces are turned on');
});

test('Everyone who signs in joins the one workspace, the first as its owner', async ({
	page,
	browser
}, testInfo) => {
	const bob = await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder'
	});
	const me = await (await bob.request.get('/api/users/me')).json();
	expect(me.workspace).toEqual(expect.objectContaining({ name: 'Default', role: 'member' }));

	// There is nothing to switch to or invite to
	await page.goto('/settings/members');
	await expect(page.getByRole('button', { name: /switch workspaces/ })).toBeHidden();
	await expect(page.getByRole('button', { name: 'Invite member' })).toBeHidden();
	expect((await page.request.post('/api/workspaces', { data: { name: 'Other' } })).status()).toBe(
		403
	);

	// Roles and ownership still work, but nobody is removed, since they would rejoin at their next sign-in
	const members = page.getByRole('table', { name: 'Members' });
	await expect(members.getByRole('row', { name: /E2E User/ })).toContainText('Owner');
	await page.getByRole('button', { name: 'Role of Bob Builder', exact: true }).click();
	await page.getByRole('option', { name: 'Admin' }).click();
	await expect(page.getByText('Bob Builder is now admin')).toBeVisible();
	await page.getByRole('button', { name: 'Actions for Bob Builder' }).click();
	await expect(page.getByRole('menuitem', { name: 'Make owner' })).toBeVisible();
	await expect(page.getByRole('menuitem', { name: 'Remove from workspace' })).toBeHidden();
});
