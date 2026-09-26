import { expect, test, type Browser, type Page, type TestInfo } from '@playwright/test';
import authUtil, { accounts, type TestAccount } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import runUtil from '../utils/run.util';
import { DEFAULT_WORKSPACE_ID, workspaceSwitcher } from '../utils/workspace.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// An ID shaped like the real ones that no job or run ever has
const MISSING_ID = '00000000-0000-7000-8000-00000000beef';

// Signs the account in once from a browser context of its own and closes that context, so the user exists without leaving a page open for the rest of the run
async function signUp(browser: Browser, testInfo: TestInfo, account: TestAccount) {
	const page = await authUtil.pageAs(browser, testInfo, account);
	await page.context().close();
}

// The crumbs of the trail in the header, which name the page and the sections above it
function breadcrumbs(page: Page) {
	return page.getByRole('navigation', { name: 'breadcrumb' }).getByRole('listitem');
}

// Checks that the in-app error page shows the title, the description and the way back, while the sidebar stays at hand
async function expectErrorPage(
	page: Page,
	{ title, description, back }: { title: string; description: string; back: string }
) {
	await expect(page.getByRole('heading', { level: 1 })).toHaveText(title);
	await expect(page.getByText(description, { exact: true })).toBeVisible();
	await expect(page.getByRole('link', { name: back, exact: true })).toBeVisible();
	await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
	await expect(page).toHaveTitle(`${title} · Umpteenth`);
}

// Opens the command palette with its shortcut and returns the dialog
// The shortcut is handled by the app shell, so the sidebar has to be there before a key press can reach it
async function openPalette(page: Page) {
	await expect(page.getByRole('button', { name: /Quick search/ })).toBeVisible();
	await page.keyboard.press('ControlOrMeta+k');
	const dialog = page.getByRole('dialog', { name: 'Command palette' });
	await expect(dialog).toBeVisible();
	return dialog;
}

// Types into the open palette and waits until the server's jobs and runs for the query have arrived
// The palette searches after a debounce and shows a spinner until then, while the rows of the previous query may still be on screen
async function searchPalette(page: Page, query: string) {
	const dialog = page.getByRole('dialog', { name: 'Command palette' });
	await dialog.getByRole('combobox').fill(query);
	await expect(dialog.getByRole('status', { name: 'Loading' })).toHaveCount(0);
	return dialog;
}

// Searches the open palette and presses Enter once the option is the top row, which is the one Enter opens
async function enterInPalette(page: Page, query: string, option: string) {
	const dialog = await searchPalette(page, query);
	await expect(dialog.getByRole('option', { name: option, exact: true })).toHaveAttribute(
		'aria-selected',
		'true'
	);
	await page.keyboard.press('Enter');
	await expect(dialog).toBeHidden();
}

test('Unknown pages and missing jobs and runs show the error page inside the app with the way back', async ({
	page
}) => {
	// An unknown URL keeps the app shell and leads back to the dashboard
	await page.goto('/does-not-exist');
	await expectErrorPage(page, {
		title: 'Page not found',
		description: "The page you're looking for doesn't exist.",
		back: 'Back to the dashboard'
	});
	await expect(breadcrumbs(page)).toHaveText(['Page not found']);
	await page.getByRole('link', { name: 'Back to the dashboard', exact: true }).click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible();

	// An unknown page inside a section leads back to that section
	await page.goto('/settings/unknown-tab');
	await expectErrorPage(page, {
		title: 'Page not found',
		description: "The page you're looking for doesn't exist.",
		back: 'Back to settings'
	});
	await page.getByRole('link', { name: 'Back to settings', exact: true }).click();
	await expect(page).toHaveURL('/settings/general');

	// A missing job names what is missing, and its trail keeps the section instead of the raw ID
	await page.goto(`/jobs/${MISSING_ID}`);
	await expectErrorPage(page, {
		title: 'Job not found',
		description: 'It may have been deleted, or it belongs to another workspace.',
		back: 'Back to jobs'
	});
	await expect(breadcrumbs(page)).toHaveText(['Jobs', 'Job not found']);
	await expect(breadcrumbs(page).getByRole('link', { name: 'Jobs' })).toHaveAttribute(
		'href',
		'/jobs'
	);
	await page.getByRole('link', { name: 'Back to jobs', exact: true }).click();
	await expect(page).toHaveURL('/jobs');
	await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible();

	// A missing run does the same for runs
	await page.goto(`/runs/${MISSING_ID}`);
	await expectErrorPage(page, {
		title: 'Run not found',
		description: 'It may have been deleted, or it belongs to another workspace.',
		back: 'Back to runs'
	});
	await expect(breadcrumbs(page)).toHaveText(['Runs', 'Run not found']);
});

test('The admin area refuses people who are not instance admins with an access error', async ({
	page
}) => {
	// Both admin pages explain why, while the trail still names where the user tried to go
	for (const [path, tab] of [
		['/admin/users', 'Users'],
		['/admin/workspaces', 'Workspaces']
	]) {
		await page.goto(path);
		await expectErrorPage(page, {
			title: "You don't have access to this page",
			description: 'Only instance admins can open the admin area.',
			back: 'Back to the dashboard'
		});
		await expect(breadcrumbs(page)).toHaveText(['Admin', tab]);
		await expect(page.getByRole('table')).toHaveCount(0);
	}
});

test('A ?workspace= link switches to a workspace the user belongs to, and changes nothing for someone who cannot open it', async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// The job lives in the default workspace, and creating a second workspace moves the session there
	const job = await runUtil.createJob(page.request, 'Shared job');
	const created = await page.request.post('/api/workspaces', { data: { name: 'Platform team' } });
	expect(created.ok()).toBeTruthy();

	// A plain link to the job finds nothing from the other workspace
	await page.goto(`/jobs/${job.id}`);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Job not found');
	await expect(workspaceSwitcher(page)).toContainText('Platform team');

	// A link naming the job's workspace moves the session there and drops the parameter
	await page.goto(`/jobs/${job.id}?workspace=${DEFAULT_WORKSPACE_ID}`);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Shared job');
	await expect(page).toHaveURL(`/jobs/${job.id}`);
	await expect(workspaceSwitcher(page)).toContainText('Default');

	// Bob isn't a member of the default workspace, so the same link leaves him in his own and shows nothing of the job
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	await bob.goto(`/jobs/${job.id}?workspace=${DEFAULT_WORKSPACE_ID}`);
	await expect(bob.getByRole('heading', { level: 1 })).toHaveText('Job not found');
	await expect(bob).toHaveURL(`/jobs/${job.id}`);
	await expect(workspaceSwitcher(bob)).toContainText("Bob Builder's workspace");
	const me = (await (await bob.request.get('/api/users/me')).json()) as {
		workspace: { name: string };
	};
	expect(me.workspace.name).toBe("Bob Builder's workspace");
	await bob.context().close();
});

test('Instance admins get only the admin tabs that apply, search users by name and email, and have no actions on their own account', async ({
	page,
	browser
}, testInfo) => {
	// Bob, Carol and the instance admin Ivy sign in, so the admin table lists them next to the e2e user
	const workspacesEnabled = await authUtil.workspacesEnabled(page);
	await signUp(browser, testInfo, accounts.bob);
	await signUp(browser, testInfo, accounts.carol);
	const ivy = await authUtil.pageAs(browser, testInfo, accounts.ivy);

	// The admin area only lists workspaces where people can have several
	await ivy.goto('/admin/users');
	await expect(ivy.getByRole('tablist', { name: 'Admin sections' }).getByRole('tab')).toHaveText(
		workspacesEnabled ? ['Users', 'Workspaces'] : ['Users']
	);

	// Only the instance admin's row carries the Admin badge
	const table = ivy.getByRole('table', { name: 'Users' });
	const ivyRow = table.getByRole('row', { name: /Ivy Admin/ });
	const bobRow = table.getByRole('row', { name: /Bob Builder/ });
	await expect(table.getByRole('row')).toHaveCount(5);
	await expect(ivyRow.getByText('Admin', { exact: true })).toBeVisible();
	await expect(bobRow.getByText('Admin', { exact: true })).toHaveCount(0);

	// Nobody locks themselves out, so the admin's own row has no actions while the others do
	await expect(ivyRow.getByRole('button', { name: 'Actions for Ivy Admin' })).toHaveCount(0);
	await expect(bobRow.getByRole('button', { name: 'Actions for Bob Builder' })).toBeVisible();

	// The API refuses it too
	const me = (await (await ivy.request.get('/api/users/me')).json()) as { id: string };
	const refused = await ivy.request.patch(`/api/admin/users/${me.id}`, {
		data: { deactivated: true }
	});
	expect(refused.status()).toBe(409);
	expect(((await refused.json()) as { message: string }).message).toBe(
		"You can't deactivate yourself"
	);

	// Search matches a word only Bob's name has and lives in the URL, so it survives a reload
	const search = ivy.getByRole('searchbox', { name: 'Search users' });
	await search.fill('builder');
	await expect(ivy).toHaveURL(/search=builder/);
	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(bobRow).toBeVisible();
	await ivy.reload();
	await expect(search).toHaveValue('builder');
	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(bobRow).toBeVisible();

	// Search matches email addresses too
	await search.fill('carol@example');
	await expect(ivy).toHaveURL(/search=carol%40example/);
	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(table.getByRole('row', { name: /Carol/ })).toBeVisible();

	// A search without matches says so and offers the way back to everyone
	await search.fill('zzz');
	await expect(ivy.getByText('No results match your search or filters')).toBeVisible();
	await ivy.getByRole('button', { name: 'Clear filters' }).click();
	await expect(search).toHaveValue('');
	await expect(ivy).not.toHaveURL(/search=/);
	await expect(table.getByRole('row')).toHaveCount(5);
	await ivy.context().close();
});

test('An instance admin loses the admin area in every session once their provider stops vouching for them', async ({
	browser
}, testInfo) => {
	// Ivy signs in as an instance admin and opens the admin area
	const ivy = await authUtil.pageAs(browser, testInfo, accounts.ivy);
	await ivy.goto('/admin/users');
	await expect(ivy.getByRole('table', { name: 'Users' })).toBeVisible();
	const sidebar = ivy.getByRole('navigation', { name: 'Main' });
	await expect(sidebar.getByRole('link', { name: 'Admin', exact: true })).toBeVisible();

	// Ivy signs in somewhere else, and this time the provider doesn't say she is an admin
	const elsewhere = await authUtil.pageAs(browser, testInfo, { ...accounts.ivy, admin: false });

	// The session from before is re-read on every request, so it loses the admin area at once
	expect((await ivy.request.get('/api/admin/users')).status()).toBe(403);
	await ivy.reload();
	await expect(ivy.getByRole('heading', { level: 1 })).toHaveText(
		"You don't have access to this page"
	);
	await expect(sidebar.getByRole('link', { name: 'Admin', exact: true })).toBeHidden();

	// The new session never had it
	await elsewhere.goto('/');
	await expect(elsewhere.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible();
	await expect(
		elsewhere
			.getByRole('navigation', { name: 'Main' })
			.getByRole('link', { name: 'Admin', exact: true })
	).toBeHidden();
	await ivy.context().close();
	await elsewhere.context().close();
});

test('Instance admins open a workspace without joining it, and lose it together with their admin rights', async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// Bob signs in once and gets a personal workspace the instance admin Ivy isn't a member of
	await signUp(browser, testInfo, accounts.bob);
	const ivy = await authUtil.pageAs(browser, testInfo, accounts.ivy);

	// The admin's own workspace is marked as the current one and needs no way in
	await ivy.goto('/admin/workspaces');
	const table = ivy.getByRole('table', { name: 'Workspaces' });
	const own = table.getByRole('row', { name: /Ivy Admin's workspace/ });
	await expect(own).toContainText('Current');
	await expect(own.getByRole('button', { name: 'Open' })).toHaveCount(0);

	// Search finds a workspace by its name, and its row names the owner
	await ivy.getByRole('searchbox', { name: 'Search workspaces' }).fill('bob');
	await expect(ivy).toHaveURL(/search=bob/);
	const bobs = table.getByRole('row', { name: /Bob Builder's workspace/ });
	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(bobs).toContainText('bob@example.com');

	// Opening it moves the session there with the owner's rights
	await bobs.getByRole('button', { name: 'Open' }).click();
	await expect(ivy).toHaveURL('/');
	await expect(workspaceSwitcher(ivy)).toContainText("Bob Builder's workspace");
	await expect(workspaceSwitcher(ivy)).toContainText('Owner');

	// A token acts with its creator's membership, which an admin who only opened the workspace doesn't have
	const token = await ivy.request.post('/api/tokens', { data: { name: 'Ivy CI' } });
	expect(token.status()).toBe(403);
	expect(((await token.json()) as { message: string }).message).toBe(
		'Only members of the workspace can create API tokens in it'
	);

	// Once she signs in without admin rights, the session in Bob's workspace has nothing left to stand on
	const elsewhere = await authUtil.pageAs(browser, testInfo, { ...accounts.ivy, admin: false });
	expect((await ivy.request.get('/api/users/me')).status()).toBe(401);
	await ivy.reload();
	await expect(ivy).toHaveURL('/login');
	await elsewhere.goto('/');
	await expect(workspaceSwitcher(elsewhere)).toContainText("Ivy Admin's workspace");
	await ivy.context().close();
	await elsewhere.context().close();
});

test('Signing out ends the session in every tab, and going back leads to the login page', async ({
	page
}) => {
	// Two tabs of the same session, each on a page of its own
	await page.goto('/jobs');
	await expect(page.getByRole('heading', { name: 'Jobs', level: 1 })).toBeVisible();
	const other = await page.context().newPage();
	await other.goto('/settings/members');
	await expect(other.getByRole('table', { name: 'Members' })).toBeVisible();

	// Signing out clears the session cookie, which every tab of this browser shares
	await page.getByRole('button', { name: 'Account menu' }).click();
	await page.getByRole('menu').getByRole('menuitem', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/login');
	expect((await page.request.get('/api/users/me')).status()).toBe(401);

	// Going back never shows the page from before, but asks to sign in and return there
	// SvelteKit doesn't update the address bar for a redirect during a back navigation, so the sign-in link tells where the page returns to
	await page.goBack();
	await expect(page.getByRole('heading', { name: 'Sign in to Umpteenth' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Sign in with Pocket ID' })).toHaveAttribute(
		'href',
		'/api/auth/login/pocket-id?redirect=%2Fjobs'
	);
	await expect(page.getByRole('heading', { name: 'Jobs' })).toHaveCount(0);

	// The other tab still shows its page, and its next navigation asks to sign in for where it was going
	await other.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Runs' }).click();
	await expect(other).toHaveURL('/login?redirect=%2Fruns');
	await expect(other.getByRole('link', { name: 'Sign in with Pocket ID' })).toBeVisible();
});

test('Signing in returns to a deep link together with its query', async ({ browser }, testInfo) => {
	// A browser of its own without a session, and a deep link with a query
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const guest = await context.newPage();
	const link = '/settings/members?search=bob';

	// Without a session the deep link goes to the login page, and every sign-in button keeps it
	await guest.goto(link);
	await expect(guest).toHaveURL(`/login?redirect=${encodeURIComponent(link)}`);
	await expect(guest.getByRole('link', { name: 'Sign in with Pocket ID' })).toHaveAttribute(
		'href',
		`/api/auth/login/pocket-id?redirect=${encodeURIComponent(link)}`
	);

	// The sign-in returns to the link, and so does the login page once there is a session
	const { redirect } = await authUtil.signInAs(guest, { redirect: link });
	expect(redirect).toBe(link);
	await guest.reload();
	await expect(guest).toHaveURL(link);
	await expect(guest.getByRole('searchbox', { name: 'Search members' })).toHaveValue('bob');
	await context.close();
});

test('Signing in never sends the browser to another site', async ({ page, browser }, testInfo) => {
	// Protocol-relative, absolute and backslash targets, and one whose tab browsers drop, which turns it protocol-relative
	const foreign = ['//evil.example', 'https://evil.example', '/\\evil.example', '/\t/evil.example'];
	// The app resolves dot segments before it navigates, which turns this one protocol-relative too, while a browser following it from the backend stays on the site
	const foreignInApp = [...foreign, '/.//evil.example'];

	// The backend never hands out a redirect to another site
	for (const target of foreign) {
		const login = await authUtil.signInAs(page, { redirect: target });
		expect(login.redirect, target).toBe('/');
	}

	// A signed-in visit to the login page goes to the app's own dashboard instead
	for (const target of foreignInApp) {
		await page.goto(`/login?redirect=${encodeURIComponent(target)}`);
		await expect(page, target).toHaveURL('/');
		await expect(page.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible();
	}

	// Without a session the sign-in buttons drop such a target instead of passing it on
	const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
	const guest = await context.newPage();
	for (const target of foreignInApp) {
		await guest.goto(`/login?redirect=${encodeURIComponent(target)}`);
		await expect(
			guest.getByRole('link', { name: 'Sign in with Pocket ID' }),
			target
		).toHaveAttribute('href', '/api/auth/login/pocket-id');
	}
	await context.close();
});

test('The command palette jumps to settings tabs by keyword, to jobs and to actions', async ({
	page
}) => {
	// A job for the palette to offer, seen from the dashboard
	const job = await runUtil.createJob(page.request, 'Nightly report');
	await page.goto('/');

	// Quick search in the sidebar opens the palette with the actions, the recent jobs and the pages
	await page.getByRole('button', { name: /Quick search/ }).click();
	const palette = page.getByRole('dialog', { name: 'Command palette' });
	await expect(palette.getByRole('option', { name: 'Create job', exact: true })).toBeVisible();
	await expect(palette.getByRole('option', { name: /Nightly report/ })).toBeVisible();
	await expect(palette.getByRole('option', { name: 'Settings › Secrets' })).toBeVisible();

	// A settings tab is found by words that aren't in its label
	await enterInPalette(page, 'invite', 'Settings › Members');
	await expect(page).toHaveURL('/settings/members');

	// A job opens its page
	await openPalette(page);
	await searchPalette(page, 'nightly');
	await palette.getByRole('option', { name: /Nightly report/ }).click();
	await expect(page).toHaveURL(`/jobs/${job.id}`);
	await expect(page.getByRole('heading', { level: 1 })).toContainText('Nightly report');

	// An action is found by its keywords too
	await openPalette(page);
	await enterInPalette(page, 'new', 'Create job');
	await expect(page).toHaveURL('/jobs/new');
	await expect(page.getByRole('heading', { name: 'New job', level: 1 })).toBeVisible();

	// A query without matches says so, and Escape closes the palette
	await openPalette(page);
	await searchPalette(page, 'zzz');
	await expect(palette.getByText('No jobs, runs or pages match "zzz"')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(palette).toBeHidden();
});

test('The command palette offers the admin pages only to instance admins', async ({ page }) => {
	// Someone who isn't an instance admin finds nothing under admin
	await page.goto('/');
	await openPalette(page);
	await searchPalette(page, 'admin');
	const palette = page.getByRole('dialog', { name: 'Command palette' });
	await expect(palette.getByText('No jobs, runs or pages match "admin"')).toBeVisible();
	await expect(palette.getByRole('option')).toHaveCount(0);
	await page.keyboard.press('Escape');

	// An instance admin gets the admin area and its tabs
	await authUtil.signInAs(page, accounts.ivy);
	await page.reload();
	await openPalette(page);
	await searchPalette(page, 'admin');
	await expect(palette.getByRole('option', { name: 'Admin', exact: true })).toBeVisible();
	await expect(palette.getByRole('option', { name: 'Admin › Users', exact: true })).toBeVisible();
	await enterInPalette(page, 'admin users', 'Admin › Users');
	await expect(page).toHaveURL('/admin/users');
	await expect(page.getByRole('table', { name: 'Users' })).toBeVisible();
});
