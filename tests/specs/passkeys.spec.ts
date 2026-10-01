import { expect, test } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { addAuthenticator, setUpWithPasskey, signOut } from '../utils/passkey.util';
import { createInviteLink, workspaceSwitcher } from '../utils/workspace.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
});

test('The first visitor sets up the instance with a passkey and signs in with it again', async ({
	page
}) => {
	const authenticator = await addAuthenticator(page);

	// A reset leaves the instance without users, so the login page creates the first account
	await page.goto('/jobs');
	await expect(page.getByRole('heading', { name: 'Set up Umpteenth' })).toBeVisible();

	// Sign-in providers stay available, since the first account can also come from one
	await expect(page.getByRole('link', { name: 'Sign in with Pocket ID' })).toBeVisible();

	await page.getByLabel('Name').fill('Ada Admin');
	await page.getByLabel('Email').fill('ada@example.com');
	await page.getByRole('button', { name: 'Create account with a passkey' }).click();
	await expect(page).toHaveURL('/jobs');
	expect(await authenticator.credentials()).toHaveLength(1);

	// The first account is an instance admin
	await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible();

	// Once someone has an account, the login page is back to signing in, and remembers the passkey
	await signOut(page);
	await expect(page.getByRole('heading', { name: 'Sign in to Umpteenth' })).toBeVisible();
	const passkeyButton = page.getByRole('button', { name: /Sign in with a passkey/ });
	await expect(passkeyButton).toContainText('Last used');
	await passkeyButton.click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
});

test('An admin adds a passkey user, who signs in through the link and adds a passkey', async ({
	page,
	browser
}, testInfo) => {
	await addAuthenticator(page);
	await setUpWithPasskey(page);

	// The admin creates the account and gets a link that only shows once
	await page.goto('/admin/users');
	await page.getByRole('button', { name: 'Add user' }).click();
	const dialog = page.getByRole('dialog', { name: 'Add user' });
	await dialog.getByLabel('Name').fill('Grace Hopper');
	await dialog.getByLabel('Email').fill('grace@example.com');
	await dialog.getByRole('button', { name: 'Add user' }).click();
	const linkDialog = page.getByRole('dialog', { name: 'Sign-in link created' });
	const url = await linkDialog.getByRole('textbox', { name: 'Sign-in link' }).inputValue();
	await linkDialog.getByRole('button', { name: 'Done' }).click();
	await expect(page.getByRole('row', { name: /Grace Hopper/ })).toContainText('Passkey');

	// The new user opens the link in their own browser and lands on their account, which asks for a passkey
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const grace = await context.newPage();
	const authenticator = await addAuthenticator(grace);
	await grace.goto(new URL(url).pathname);
	await expect(grace).toHaveURL('/account');
	await expect(grace.getByText('Add a passkey to sign in again')).toBeVisible();
	await grace.getByRole('button', { name: 'Add passkey' }).click();
	await expect(grace.getByRole('table', { name: 'Passkeys' })).toContainText('Passkey');
	expect(await authenticator.credentials()).toHaveLength(1);

	// The link worked once, and the passkey signs in from then on
	await signOut(grace);
	await grace.goto(new URL(url).pathname);
	await expect(
		grace.getByRole('heading', { name: 'This sign-in link no longer works' })
	).toBeVisible();
	await grace.goto('/login');
	await grace.getByRole('button', { name: /Sign in with a passkey/ }).click();
	await expect(grace).toHaveURL('/');
	await grace.getByRole('button', { name: 'Account menu' }).click();
	await expect(grace.getByRole('menu')).toContainText('grace@example.com');
	await context.close();
});

test('Someone with an invite link creates a passkey account and joins the workspace', async ({
	page,
	browser
}, testInfo) => {
	await addAuthenticator(page);
	await setUpWithPasskey(page);
	test.skip(!(await authUtil.workspacesEnabled(page)), 'Invites need workspaces');
	const invite = await createInviteLink(page.request);

	// The invite sends a newcomer to sign in, where they can create an account instead
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const newcomer = await context.newPage();
	await addAuthenticator(newcomer);
	await newcomer.goto(invite);
	await expect(newcomer.getByRole('heading', { name: 'Sign in to Umpteenth' })).toBeVisible();
	await newcomer.getByRole('button', { name: 'Create an account' }).click();
	await newcomer.getByLabel('Name').fill('Nora Newcomer');
	await newcomer.getByRole('button', { name: 'Create account with a passkey' }).click();

	// They land in the invite's workspace as a member, not as an instance admin
	await expect(workspaceSwitcher(newcomer)).toContainText('Member');
	await expect(newcomer.getByRole('link', { name: 'Admin' })).toBeHidden();
	await context.close();

	// Without an invite nobody else can sign up any more
	const response = await page.request.post('/api/auth/passkey/sign-up/options', {
		data: { name: 'Mallory' }
	});
	expect(response.status()).toBe(403);
});

test('Passkeys can be renamed, and the last one stays', async ({ page }) => {
	await addAuthenticator(page);
	await setUpWithPasskey(page);

	await page.getByRole('button', { name: 'Account menu' }).click();
	await page.getByRole('menuitem', { name: 'Account' }).click();
	await expect(page).toHaveURL('/account');

	// The only passkey offers no way to remove it
	const table = page.getByRole('table', { name: 'Passkeys' });
	await table.getByRole('button', { name: /Actions for/ }).click();
	await expect(page.getByRole('menuitem', { name: 'Remove' })).toBeHidden();
	await page.getByRole('menuitem', { name: 'Rename' }).click();
	const dialog = page.getByRole('dialog', { name: 'Rename passkey' });
	await dialog.getByLabel('Name').fill('Work laptop');
	await dialog.getByRole('button', { name: 'Rename' }).click();
	await expect(table).toContainText('Work laptop');

	// The profile keeps the name the menu shows
	await page.getByLabel('Name').first().fill('Ada Lovelace');
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Changes saved')).toBeVisible();
	await page.getByRole('button', { name: 'Account menu' }).click();
	await expect(page.getByRole('menu')).toContainText('Ada Lovelace');
});

test('Accounts of sign-in providers get no passkeys or sign-in links', async ({ page }) => {
	await authUtil.signInAs(page, { admin: true });
	await page.goto('/');
	await page.getByRole('button', { name: 'Account menu' }).click();
	await expect(page.getByRole('menuitem', { name: 'Account' })).toBeHidden();
	await page.keyboard.press('Escape');

	// An admin's own row has no actions, and someone from a sign-in provider has no passkey ones
	await authUtil.signInAs(page, { subject: 'bob', name: 'Bob Builder', email: 'bob@example.com' });
	await authUtil.signInAs(page, { admin: true });
	await page.goto('/admin/users');
	await page
		.getByRole('row', { name: /Bob Builder/ })
		.getByRole('button', { name: /Actions for/ })
		.click();
	await expect(page.getByRole('menuitem', { name: 'Create sign-in link' })).toBeHidden();
	await expect(page.getByRole('menuitem', { name: 'Deactivate' })).toBeVisible();
});
