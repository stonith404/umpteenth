import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

test('Signed-in user sees the app shell', async ({ page }) => {
	await page.goto('/');

	const sidebar = page.locator('[data-slot="sidebar"]');
	for (const item of ['Dashboard', 'Runs', 'Jobs', 'MCP servers', 'Settings']) {
		await expect(sidebar.getByRole('link', { name: item, exact: true })).toBeVisible();
	}
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Account menu' })).toBeVisible();
});

test('Create, search and delete an API token', async ({ page }) => {
	await page.goto('/');

	// Navigate to the API tokens tab through the sidebar and the settings tabs
	await page.getByRole('link', { name: 'Settings', exact: true }).click();
	await expect(page).toHaveURL('/settings/general');
	await page.getByRole('tab', { name: 'API tokens' }).click();
	await expect(page).toHaveURL('/settings/tokens');
	await expect(page.getByText('No API tokens')).toBeVisible();

	// Create a token and check it is shown exactly once
	await page.getByRole('button', { name: 'Create API token' }).click();
	const createDialog = page.getByRole('dialog', { name: 'Create API token' });
	await createDialog.getByLabel('Name').fill('Smoke test token');
	await createDialog.getByRole('button', { name: 'Create API token' }).click();

	const createdDialog = page.getByRole('dialog', { name: 'API token created' });
	await expect(createdDialog.getByRole('textbox', { name: 'Smoke test token' })).toHaveValue(
		/^ump_/
	);
	await createdDialog.getByRole('button', { name: 'Done' }).click();
	await expect(createdDialog).toBeHidden();

	// A second token makes the search meaningful
	const response = await page.request.post('/api/tokens', { data: { name: 'Other token' } });
	expect(response.ok()).toBeTruthy();
	await page.reload();

	const table = page.getByRole('table', { name: 'API tokens' });
	await expect(table.getByRole('row', { name: /Smoke test token/ })).toBeVisible();
	await expect(table.getByRole('row', { name: /Other token/ })).toBeVisible();

	// Search on the server and keep the search in the URL
	await page.getByRole('searchbox', { name: 'Search tokens' }).fill('smoke');
	await expect(page).toHaveURL(/search=smoke/);
	await expect(table.getByRole('row', { name: /Other token/ })).toBeHidden();
	await expect(table.getByRole('row', { name: /Smoke test token/ })).toBeVisible();

	await page.reload();
	await expect(page.getByRole('searchbox', { name: 'Search tokens' })).toHaveValue('smoke');
	await expect(table.getByRole('row', { name: /Other token/ })).toBeHidden();

	// Delete the token through the confirm dialog
	await table.getByRole('button', { name: 'Actions for Smoke test token' }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const confirmDialog = page.getByRole('alertdialog');
	await expect(confirmDialog).toContainText('Smoke test token');
	await confirmDialog.getByRole('button', { name: 'Delete' }).click();

	await expect(table.getByRole('row', { name: /Smoke test token/ })).toBeHidden();
	await expect(page.getByText('No results match your search or filters')).toBeVisible();
});
