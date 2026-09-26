import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
});

test('Login page offers every sign-in provider with the primary one first', async ({ page }) => {
	await page.goto('/jobs');
	await expect(page).toHaveURL('/login?redirect=%2Fjobs');

	// The primary provider comes first, the others follow by name
	const links = page.getByRole('link', { name: /^Sign in with/ });
	await expect(links).toHaveText([
		'Sign in with Pocket ID',
		'Sign in with Corp SSO',
		'Sign in with GitHub'
	]);

	// Each button starts the login with its own provider and keeps the page to return to
	await expect(links.first()).toHaveAttribute('href', '/api/auth/login/pocket-id?redirect=%2Fjobs');
	await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveAttribute(
		'href',
		'/api/auth/login/github?redirect=%2Fjobs'
	);

	// Nothing was used before in this browser
	await expect(page.getByText('Last used')).toBeHidden();
});

test('Login page points out the provider used last', async ({ page }) => {
	await authUtil.authenticate(page, 'github');
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

	await page.getByRole('button', { name: 'Account menu' }).click();
	await page.getByRole('menuitem', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/login');

	await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toContainText('Last used');
	await expect(page.getByText('Last used')).toHaveCount(1);
});

test('Login page explains an unknown provider', async ({ page }) => {
	await page.goto('/api/auth/login/removed');
	await expect(page).toHaveURL('/login?error=not_found');
	await expect(page.getByText('This sign-in option is no longer available')).toBeVisible();
});
