import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
});

test('Only instance admins see the admin area', async ({ page }) => {
	await authUtil.authenticate(page);
	await page.goto('/');
	await expect(page.getByRole('link', { name: 'Admin', exact: true })).toBeHidden();
	const refused = await page.request.get('/api/admin/users');
	expect(refused.status()).toBe(403);

	await authUtil.signInAs(page, {
		subject: 'ivy',
		email: 'ivy@example.com',
		name: 'Ivy Admin',
		admin: true
	});
	await page.goto('/');
	await page.getByRole('link', { name: 'Admin', exact: true }).click();
	await expect(page).toHaveURL('/admin/users');
	await expect(
		page.getByRole('table', { name: 'Users' }).getByRole('row', { name: /Ivy Admin/ })
	).toContainText('Admin');
});

test('A deactivated user is signed out everywhere and can sign in again once reactivated', async ({
	page,
	browser
}, testInfo) => {
	const bob = await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder'
	});
	await authUtil.signInAs(page, {
		subject: 'ivy',
		email: 'ivy@example.com',
		name: 'Ivy Admin',
		admin: true
	});

	// Deactivating ends Bob's session at once
	await page.goto('/admin/users');
	const bobRow = page
		.getByRole('table', { name: 'Users' })
		.getByRole('row', { name: /Bob Builder/ });
	await bobRow.getByRole('button', { name: 'Actions for Bob Builder' }).click();
	await page.getByRole('menuitem', { name: 'Deactivate' }).click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Deactivate' }).click();
	await expect(bobRow).toContainText('Deactivated');
	expect((await bob.request.get('/api/users/me')).status()).toBe(401);
	const signIn = await bob.request.post('/api/test/session', {
		data: { subject: 'bob', email: 'bob@example.com', name: 'Bob Builder' }
	});
	expect(signIn.status()).toBe(403);

	// Reactivated, he can sign in again
	await bobRow.getByRole('button', { name: 'Actions for Bob Builder' }).click();
	await page.getByRole('menuitem', { name: 'Reactivate' }).click();
	await expect(bobRow).not.toContainText('Deactivated');
	await authUtil.signInAs(bob, { subject: 'bob', email: 'bob@example.com', name: 'Bob Builder' });
	expect((await bob.request.get('/api/users/me')).ok()).toBeTruthy();
});

test('Instance admins open and delete any workspace', async ({ page, browser }, testInfo) => {
	await authUtil.authenticate(page);
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// Bob has a workspace of his own that the admin isn't a member of
	await authUtil.pageAs(browser, testInfo, {
		subject: 'bob',
		email: 'bob@example.com',
		name: 'Bob Builder'
	});
	await authUtil.signInAs(page, {
		subject: 'ivy',
		email: 'ivy@example.com',
		name: 'Ivy Admin',
		admin: true
	});
	await page.goto('/admin/workspaces');
	const table = page.getByRole('table', { name: 'Workspaces' });
	const bobs = table.getByRole('row', { name: /Bob Builder's workspace/ });
	await bobs.getByRole('button', { name: 'Open' }).click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('button', { name: /switch workspaces/ })).toContainText(
		"Bob Builder's workspace"
	);

	await page.goto('/admin/workspaces');
	await bobs.getByRole('button', { name: "Actions for Bob Builder's workspace" }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	await page.getByRole('alertdialog').getByRole('textbox').fill("Bob Builder's workspace");
	await page.getByRole('alertdialog').getByRole('button', { name: 'Delete workspace' }).click();
	await expect(page.getByText("Deleted Bob Builder's workspace")).toBeVisible();
	await expect(page).toHaveURL('/');
	await page.goto('/admin/workspaces');
	await expect(bobs).toBeHidden();
});
