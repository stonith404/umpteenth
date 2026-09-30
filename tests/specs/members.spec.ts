import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import {
	createInviteLink,
	DEFAULT_WORKSPACE_ID,
	joinWorkspace,
	workspaceSwitcher
} from '../utils/workspace.util';

// Removing people, invites and moving between workspaces all need several workspaces, so these specs only run with workspaces turned on
test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');
});

// The other users' pages would keep reconnecting to a backend the next test resets, so their browser contexts close after every test, a failed one included
test.afterEach(async ({ browser, page }) => {
	const others = browser.contexts().filter((context) => context !== page.context());
	await Promise.all(others.map((context) => context.close()));
});

// Opens the invite dialog of the members page, which starts on inviting by email
async function openInviteDialog(page: Page) {
	await page.getByRole('button', { name: 'Invite member' }).click();
	return page.getByRole('dialog', { name: 'Invite to workspace' });
}

// Invites an address through the API, which always leaves an invite waiting for the next sign-in with it, and returns the answer
async function inviteEmail(request: APIRequestContext, email: string) {
	const response = await request.post('/api/workspace/invites', {
		data: { email, role: 'member', expiresInDays: 7 }
	});
	expect(response.ok()).toBeTruthy();
	return response.json();
}

// The user ID of the only member whose name or address matches the search
async function memberId(request: APIRequestContext, search: string) {
	const response = await request.get('/api/workspace/members', { params: { search } });
	expect(response.ok()).toBeTruthy();
	const { items } = (await response.json()) as { items: { userId: string }[] };
	expect(items, `members matching ${search}`).toHaveLength(1);
	return items[0].userId;
}

// The workspace's pending invites, where a link has no email address
async function listInvites(request: APIRequestContext) {
	const response = await request.get('/api/workspace/invites');
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as {
		id: string;
		email: string | null;
		role: string;
		invitedBy: string | null;
		createdAt: number;
		expiresAt: number;
	}[];
}

// The workspaces the signed-in user belongs to, with their role in each
async function listWorkspaces(request: APIRequestContext) {
	const response = await request.get('/api/workspaces');
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { id: string; name: string; role: string }[];
}

test('Removing a member locks them out at once, API tokens included, and their next sign-in lands in a workspace of their own', async ({
	page,
	browser,
	request
}, testInfo) => {
	// Bob joins, makes a token for his scripts and has a page open
	const bob = await joinWorkspace(browser, testInfo, page, accounts.bob);
	const bobToken = authUtil.bearer((await authUtil.createToken(bob.request, 'Bob CI')).token);
	expect((await request.get('/api/jobs', bobToken)).ok()).toBeTruthy();
	await bob.goto('/runs');
	await expect(workspaceSwitcher(bob)).toContainText('Default');

	// The owner removes him from the members table
	await page.goto('/settings/members');
	const members = page.getByRole('table', { name: 'Members' });
	await members.getByRole('button', { name: 'Actions for Bob Builder' }).click();
	await page.getByRole('menuitem', { name: 'Remove from workspace' }).click();
	await page
		.getByRole('alertdialog', { name: 'Remove Bob Builder' })
		.getByRole('button', { name: 'Remove' })
		.click();
	await expect(page.getByText('Removed Bob Builder')).toBeVisible();
	await expect(members.getByRole('row', { name: /Bob Builder/ })).toBeHidden();

	// Bob's open session ends right away, so his next page load asks him to sign in and brings him back afterwards
	expect((await bob.request.get('/api/users/me')).status()).toBe(401);
	await bob.reload();
	await expect(bob).toHaveURL('/login?redirect=%2Fruns');

	// His token stops working too
	const refused = await request.get('/api/jobs', bobToken);
	expect(refused.status()).toBe(401);
	expect(await refused.json()).toMatchObject({ code: 'invalid_token' });

	// Signing in again gives him a workspace of his own, since he belongs to none
	await authUtil.signInAs(bob, accounts.bob);
	await bob.goto('/');
	await expect(workspaceSwitcher(bob)).toContainText("Bob Builder's workspace");
	expect(await listWorkspaces(bob.request)).toEqual([
		expect.objectContaining({ name: "Bob Builder's workspace", role: 'owner' })
	]);
});

test('Admins manage members but can neither hand the workspace over nor delete it', async ({
	page,
	browser
}, testInfo) => {
	// Bob joins as an admin and Carol as a member
	const bob = await joinWorkspace(browser, testInfo, page, accounts.bob, 'admin');
	await joinWorkspace(browser, testInfo, page, accounts.carol);

	// Bob may invite and change roles, but the owner's row and his own have no actions
	await bob.goto('/settings/members');
	const members = bob.getByRole('table', { name: 'Members' });
	await expect(bob.getByRole('button', { name: 'Invite member' })).toBeVisible();
	await expect(members.getByRole('button', { name: 'Role of Carol', exact: true })).toBeVisible();
	await expect(members.getByRole('button', { name: 'Role of E2E User', exact: true })).toHaveCount(
		0
	);
	await expect(members.getByRole('button', { name: 'Actions for E2E User' })).toHaveCount(0);
	await expect(members.getByRole('button', { name: 'Actions for Bob Builder' })).toHaveCount(0);

	// He removes Carol, whom he may not make the owner
	await members.getByRole('button', { name: 'Actions for Carol' }).click();
	const remove = bob.getByRole('menuitem', { name: 'Remove from workspace' });
	await expect(remove).toBeVisible();
	await expect(bob.getByRole('menuitem', { name: 'Make owner' })).toBeHidden();
	await remove.click();
	await bob
		.getByRole('alertdialog', { name: 'Remove Carol' })
		.getByRole('button', { name: 'Remove' })
		.click();
	await expect(bob.getByText('Removed Carol')).toBeVisible();
	await expect(members.getByRole('row', { name: /Carol/ })).toBeHidden();

	// He can leave the workspace but not delete it
	await bob.goto('/settings/general');
	await expect(bob.getByRole('button', { name: 'Leave workspace' })).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Delete workspace' })).toBeHidden();
});

test("Only the owner hands the workspace over or deletes it, and the owner can't be demoted, removed or leave", async ({
	page,
	browser
}, testInfo) => {
	// Bob joins as an admin
	const bob = await joinWorkspace(browser, testInfo, page, accounts.bob, 'admin');
	const ownerId = await memberId(page.request, 'e2e@example.com');
	const bobId = await memberId(page.request, 'bob');

	// The API refuses him the owner's operations
	const transfer = await bob.request.post('/api/workspace/transfer', { data: { userId: bobId } });
	expect(transfer.status()).toBe(403);
	expect(await transfer.json()).toMatchObject({
		message: 'Only the owner of the workspace can do this'
	});
	const deleted = await bob.request.delete('/api/workspace');
	expect(deleted.status()).toBe(403);
	expect(await deleted.json()).toMatchObject({
		message: 'Only the owner of the workspace can do this'
	});

	// The owner can't be demoted or removed, not even by an admin
	const demoted = await bob.request.patch(`/api/workspace/members/${ownerId}`, {
		data: { role: 'member' }
	});
	expect(demoted.status()).toBe(403);
	expect(await demoted.json()).toMatchObject({
		message: "The owner's role changes by handing over ownership"
	});
	const removed = await bob.request.delete(`/api/workspace/members/${ownerId}`);
	expect(removed.status()).toBe(403);
	expect(await removed.json()).toMatchObject({
		message: "The owner can't be removed, only hand over ownership"
	});

	// The owner can't leave without handing over first, so the danger zone offers deleting instead
	const left = await page.request.post('/api/workspace/leave');
	expect(left.status()).toBe(409);
	expect(await left.json()).toMatchObject({
		message: 'Hand over ownership before leaving the workspace'
	});
	await page.goto('/settings/general');
	await expect(page.getByRole('button', { name: 'Delete workspace' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Leave workspace' })).toBeHidden();

	// Nothing changed hands
	expect(await listWorkspaces(page.request)).toEqual([
		expect.objectContaining({ id: DEFAULT_WORKSPACE_ID, role: 'owner' })
	]);
	expect(await listWorkspaces(bob.request)).toEqual([
		expect.objectContaining({ id: DEFAULT_WORKSPACE_ID, role: 'admin' })
	]);
});

test('A member who leaves lands in the workspace they already had instead of a new one', async ({
	page,
	browser
}, testInfo) => {
	// Carol has a workspace of her own and joins the default one at her next sign-in
	const carol = await authUtil.pageAs(browser, testInfo, accounts.carol);
	await inviteEmail(page.request, 'carol@example.com');
	await authUtil.signInAs(carol, accounts.carol);
	await carol.goto('/');
	await workspaceSwitcher(carol).click();
	await carol.getByRole('menuitem', { name: 'Default' }).click();
	await expect(workspaceSwitcher(carol)).toContainText('Default');
	await expect(workspaceSwitcher(carol)).toContainText('Member');

	// She leaves it from the danger zone
	await carol.goto('/settings/general');
	await carol.getByRole('button', { name: 'Leave workspace' }).click();
	await carol
		.getByRole('alertdialog', { name: 'Leave Default' })
		.getByRole('button', { name: 'Leave workspace' })
		.click();

	// She is back in her own workspace, and no second personal one was made for her
	await expect(carol.getByText('Left Default')).toBeVisible();
	await expect(carol).toHaveURL('/');
	await expect(workspaceSwitcher(carol)).toContainText("Carol's workspace");
	await expect(workspaceSwitcher(carol)).toContainText('Owner');
	expect(await listWorkspaces(carol.request)).toEqual([
		expect.objectContaining({ name: "Carol's workspace", role: 'owner' })
	]);

	// The owner no longer sees her among the members
	await page.goto('/settings/members');
	const members = page.getByRole('table', { name: 'Members' });
	await expect(members.getByRole('row', { name: /E2E User/ })).toBeVisible();
	await expect(members.getByRole('row', { name: /Carol/ })).toBeHidden();
});

test('Inviting the same address again replaces its pending invite', async ({ page }) => {
	// Carol, who never signed in, has an invite as a member
	await inviteEmail(page.request, 'carol@example.com');
	await page.goto('/settings/members');
	const carolInvite = page
		.getByRole('table', { name: 'Pending invites' })
		.getByRole('row', { name: /carol@example\.com/ });
	await expect(carolInvite).toContainText('Member');

	// The owner invites her again as an admin for longer
	const dialog = await openInviteDialog(page);
	await dialog.getByLabel('Email').fill('carol@example.com');
	await dialog.getByRole('button', { name: 'Role', exact: true }).click();
	await page.getByRole('option', { name: 'Admin' }).click();
	await dialog.getByRole('button', { name: 'Expires after' }).click();
	await page.getByRole('option', { name: '30 days' }).click();
	await dialog.getByRole('button', { name: 'Invite', exact: true }).click();
	await expect(page.getByText('carol@example.com joins the next time they sign in')).toBeVisible();
	await expect(carolInvite).toContainText('Admin');

	// She still has one invite, which now carries the new role and expiry
	const invites = await listInvites(page.request);
	expect(invites).toEqual([expect.objectContaining({ email: 'carol@example.com', role: 'admin' })]);
	expect(invites[0].expiresAt - invites[0].createdAt).toBe(30 * 24 * 60 * 60 * 1000);
});

test('Revoked invites stop working', async ({ page, browser }, testInfo) => {
	// The owner has an invite link out and has invited Carol, who never signed in
	const invitePath = await createInviteLink(page.request);
	await inviteEmail(page.request, 'carol@example.com');

	// The owner revokes both, and the emptied list goes away
	await page.goto('/settings/members');
	const pending = page.getByRole('table', { name: 'Pending invites' });
	for (const invite of ['invite link', 'carol@example.com']) {
		// The toast of the first revoke can cover the next row's menu button, so the menu opens from the keyboard
		const actions = pending.getByRole('button', { name: `Actions for ${invite}` });
		await actions.press('Enter');
		await page.getByRole('menuitem', { name: 'Revoke' }).click();
		const confirm = page.getByRole('alertdialog', { name: 'Revoke invite' });
		await confirm.getByRole('button', { name: 'Revoke' }).click();
		await expect(confirm).toBeHidden();
		await expect(actions).toBeHidden();
	}
	await expect(page.getByRole('heading', { name: 'Pending invites' })).toBeHidden();
	expect(await listInvites(page.request)).toEqual([]);

	// The revoked link says so and can't be accepted
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	await bob.goto(invitePath);
	await expect(bob.getByRole('heading', { name: 'This invite no longer works' })).toBeVisible();
	const token = invitePath.split('/').pop();
	expect((await bob.request.post('/api/invites/accept', { data: { token } })).status()).toBe(404);

	// Carol signs in with the verified address and only gets a workspace of her own
	const carol = await authUtil.pageAs(browser, testInfo, accounts.carol);
	expect(await listWorkspaces(carol.request)).toEqual([
		expect.objectContaining({ name: "Carol's workspace", role: 'owner' })
	]);
});

test('The invite dialog explains an empty or malformed address next to the field', async ({
	page
}) => {
	await page.goto('/settings/members');
	const dialog = await openInviteDialog(page);
	const invite = dialog.getByRole('button', { name: 'Invite', exact: true });

	// An empty address is required
	await invite.click();
	await expect(dialog.getByRole('alert')).toHaveText('Required');
	await expect(dialog.getByLabel('Email')).toHaveAttribute('aria-invalid', 'true');

	// One without a domain ending isn't an address
	await dialog.getByLabel('Email').fill('carol@example');
	await invite.click();
	await expect(dialog.getByRole('alert')).toHaveText('Must be an email address');
});

test("An empty or malformed address never reaches the server, and doesn't block a link made after switching", async ({
	page
}) => {
	// The first invite the server gets is the link at the end
	await page.goto('/settings/members');
	const firstInvite = page.waitForRequest(
		(r) => r.method() === 'POST' && r.url().endsWith('/api/workspace/invites')
	);
	const dialog = await openInviteDialog(page);
	const invite = dialog.getByRole('button', { name: 'Invite', exact: true });
	await invite.click();
	await dialog.getByLabel('Email').fill('carol@example');
	await invite.click();

	// A link has no address, so the malformed one typed before is dropped instead of failing validation
	await dialog.getByRole('tab', { name: 'Invite link' }).click();
	await dialog.getByRole('button', { name: 'Create link' }).click();
	await expect(page.getByRole('dialog', { name: 'Invite link created' })).toBeVisible();
	expect((await firstInvite).postDataJSON()).toEqual({ role: 'member', expiresInDays: 7 });
});

test('Inviting someone who signed in before answers like any other address, and only their next verified sign-in joins them', async ({
	page,
	browser
}, testInfo) => {
	// Carol signed in before with a verified address, Dave with one his provider didn't vouch for
	const daveAccount = {
		subject: 'dave',
		email: 'dave@example.com',
		emailVerified: false,
		name: 'Dave'
	};
	const carol = await authUtil.pageAs(browser, testInfo, accounts.carol);
	const dave = await authUtil.pageAs(browser, testInfo, daveAccount);

	// Inviting Carol as an admin leaves an invite waiting instead of adding her, so nobody learns she has an account
	await page.goto('/settings/members');
	const dialog = await openInviteDialog(page);
	await dialog.getByLabel('Email').fill('carol@example.com');
	await dialog.getByRole('button', { name: 'Role', exact: true }).click();
	await page.getByRole('option', { name: 'Admin' }).click();
	await dialog.getByRole('button', { name: 'Invite', exact: true }).click();
	await expect(page.getByText('carol@example.com joins the next time they sign in')).toBeVisible();
	await expect(
		page
			.getByRole('table', { name: 'Pending invites' })
			.getByRole('row', { name: /carol@example\.com/ })
	).toContainText('Admin');
	await expect(
		page.getByRole('table', { name: 'Members' }).getByRole('row', { name: /Carol/ })
	).toBeHidden();
	expect(await inviteEmail(page.request, 'dave@example.com')).toEqual(
		await inviteEmail(page.request, 'nobody@example.com')
	);

	// Carol's next sign-in joins her, while Dave's unverified address keeps him out
	await authUtil.signInAs(carol, accounts.carol);
	await authUtil.signInAs(dave, daveAccount);
	expect(await listWorkspaces(dave.request)).toEqual([
		expect.objectContaining({ name: "Dave's workspace" })
	]);

	// Joining doesn't move Carol's session, but the workspace is hers to switch to as an admin
	await carol.goto('/');
	await expect(workspaceSwitcher(carol)).toContainText("Carol's workspace");
	await workspaceSwitcher(carol).click();
	await carol.getByRole('menuitem', { name: 'Default' }).click();
	await expect(workspaceSwitcher(carol)).toContainText('Default');
	await expect(workspaceSwitcher(carol)).toContainText('Admin');
});

test('Inviting someone who is a member already is refused and keeps the dialog open', async ({
	page,
	browser
}, testInfo) => {
	// Carol is a member already
	await joinWorkspace(browser, testInfo, page, accounts.carol);

	// Inviting her address again is refused with the reason, and the dialog stays for another address
	await page.goto('/settings/members');
	const dialog = await openInviteDialog(page);
	await dialog.getByLabel('Email').fill('carol@example.com');
	const refusal = page.waitForResponse(
		(r) => r.request().method() === 'POST' && r.url().endsWith('/api/workspace/invites')
	);
	await dialog.getByRole('button', { name: 'Invite', exact: true }).click();
	const refused = await refusal;
	expect(refused.status()).toBe(409);
	expect(await refused.json()).toMatchObject({ code: 'conflict' });
	await expect(page.getByText('That person is already a member of this workspace')).toBeVisible();
	await expect(dialog).toBeVisible();
});

test('A write from a tab that still shows the previous workspace is refused, and the tab catches up', async ({
	page
}) => {
	// Tab A shows the members of the default workspace
	await page.goto('/settings/members');
	await expect(workspaceSwitcher(page)).toContainText('Default');

	// Tab B shares the session and moves it into a new workspace
	const tabB = await page.context().newPage();
	await tabB.goto('/');
	await workspaceSwitcher(tabB).click();
	await tabB.getByRole('menuitem', { name: 'Create workspace' }).click();
	const create = tabB.getByRole('dialog', { name: 'Create workspace' });
	await create.getByLabel('Name').fill('Platform team');
	await create.getByRole('button', { name: 'Create workspace' }).click();
	await expect(tabB.getByText('Created Platform team')).toBeVisible();
	await expect(workspaceSwitcher(tabB)).toContainText('Platform team');

	// Tab A still shows the default workspace, so its invite would land in the wrong one and is refused
	await expect(workspaceSwitcher(page)).toContainText('Default');
	const dialog = await openInviteDialog(page);
	await dialog.getByRole('tab', { name: 'Invite link' }).click();
	const refusal = page.waitForResponse(
		(r) => r.request().method() === 'POST' && r.url().endsWith('/api/workspace/invites')
	);
	await dialog.getByRole('button', { name: 'Create link' }).click();
	const refused = await refusal;
	expect(refused.status()).toBe(409);
	expect(await refused.json()).toMatchObject({ code: 'workspace_changed' });
	await expect(page.getByText('Failed to invite')).toBeVisible();
	await expect(
		page.getByText(
			'Your session moved to another workspace in another tab, reload the page to continue.'
		)
	).toBeVisible();

	// Tab A then catches up with the workspace the session is in
	await expect(page).toHaveURL('/');
	await expect(workspaceSwitcher(page)).toContainText('Platform team');

	// Neither workspace got the invite
	const platformId = (await listWorkspaces(page.request)).find(
		(workspace) => workspace.name === 'Platform team'
	)!.id;
	expect(await listInvites(page.request)).toEqual([]);
	const back = await page.request.post(`/api/workspaces/${DEFAULT_WORKSPACE_ID}/switch`);
	expect(back.ok()).toBeTruthy();
	expect(await listInvites(page.request)).toEqual([]);

	// Tab B now shows a workspace the session left, and moving on from there is still allowed
	await expect(workspaceSwitcher(tabB)).toContainText('Platform team');
	await workspaceSwitcher(tabB).click();
	const switched = tabB.waitForResponse(
		(r) => r.request().method() === 'POST' && r.url().endsWith('/switch')
	);
	await tabB.getByRole('menuitem', { name: 'Default' }).click();
	const response = await switched;
	expect(await response.request().headerValue('x-umpteenth-workspace')).toBe(platformId);
	expect(response.ok()).toBeTruthy();
	await expect(workspaceSwitcher(tabB)).toContainText('Default');
});

test('The invite page shows who is signed in and what they would join, and never adds an existing user without asking', async ({
	page,
	browser
}, testInfo) => {
	// The workspace gets a name of its own, which the invite page shows
	const renamed = await page.request.patch('/api/workspace', { data: { name: 'Acme Ops' } });
	expect(renamed.ok()).toBeTruthy();
	const invitePath = await createInviteLink(page.request);

	// The owner opening their own link is told they are in already, and opening the workspace leaves the link unused
	await page.goto(invitePath);
	await expect(page.getByRole('heading', { name: "You're in Acme Ops" })).toBeVisible();
	await expect(page.getByText('Signed in as e2e@example.com')).toBeVisible();
	await page.getByRole('button', { name: 'Open workspace' }).click();
	await expect(page).toHaveURL('/');
	expect(await listInvites(page.request)).toHaveLength(1);

	// Bob has a workspace already, so a sign-in from the link returns to the invite page instead of joining him
	const bob = await authUtil.pageAs(browser, testInfo, accounts.bob);
	const login = await authUtil.signInAs(bob, { ...accounts.bob, redirect: invitePath });
	expect(login.redirect).toBe(invitePath);
	expect(await listWorkspaces(bob.request)).toEqual([
		expect.objectContaining({ name: "Bob Builder's workspace" })
	]);

	// The page shows what he would join and as whom, and 'Not now' leaves the invite alone
	await bob.goto(invitePath);
	await expect(bob.getByRole('heading', { name: 'Join Acme Ops' })).toBeVisible();
	await expect(bob.getByText('Signed in as bob@example.com')).toBeVisible();
	await bob.getByRole('link', { name: 'Not now' }).click();
	await expect(bob).toHaveURL('/');
	await expect(workspaceSwitcher(bob)).toContainText("Bob Builder's workspace");
	expect(await listInvites(page.request)).toHaveLength(1);

	// 'Use another account' signs him out, and the login page keeps the invite to return to
	await bob.goto(invitePath);
	await bob.getByRole('button', { name: 'Use another account' }).click();
	await expect(bob).toHaveURL(`/login?redirect=${encodeURIComponent(invitePath)}`);
	expect((await bob.request.get('/api/users/me')).status()).toBe(401);
});

test('An admin who makes themselves a member loses the admin controls at once', async ({
	page,
	browser
}, testInfo) => {
	// Bob joins as an admin
	const bob = await joinWorkspace(browser, testInfo, page, accounts.bob, 'admin');
	const bobId = await memberId(page.request, 'bob');

	// Bob, an admin, may invite and change roles
	await bob.goto('/settings/members');
	await expect(bob.getByRole('button', { name: 'Invite member' })).toBeVisible();
	await expect(workspaceSwitcher(bob)).toContainText('Admin');

	// He makes himself a member
	await bob.getByRole('button', { name: 'Role of Bob Builder', exact: true }).click();
	await bob.getByRole('option', { name: 'Member' }).click();
	await expect(bob.getByText('Bob Builder is now member')).toBeVisible();

	// The page drops every admin control without a reload
	await expect(bob.getByRole('button', { name: 'Invite member' })).toBeHidden();
	await expect(bob.getByRole('button', { name: /^Role of / })).toHaveCount(0);
	await expect(workspaceSwitcher(bob)).toContainText('Member');
	await expect(
		bob.getByRole('table', { name: 'Members' }).getByRole('row', { name: /Bob Builder/ })
	).toContainText('Member');

	// He can't make himself an admin again
	const promoted = await bob.request.patch(`/api/workspace/members/${bobId}`, {
		data: { role: 'admin' }
	});
	expect(promoted.status()).toBe(403);
	expect(await promoted.json()).toMatchObject({
		message: 'Only admins of the workspace can do this'
	});

	// The owner sees him as a member
	await page.goto('/settings/members');
	await expect(page.getByRole('button', { name: 'Role of Bob Builder', exact: true })).toHaveText(
		'Member'
	);
});

test('An admin who makes themselves a member no longer sees the pending invites', async ({
	page,
	browser
}, testInfo) => {
	// Bob joins as an admin, and Erin has an invite waiting
	const bob = await joinWorkspace(browser, testInfo, page, accounts.bob, 'admin');
	await inviteEmail(page.request, 'erin@example.com');

	// Bob sees the pending invite while he is an admin
	await bob.goto('/settings/members');
	const pending = bob.getByRole('table', { name: 'Pending invites' });
	await expect(pending.getByRole('row', { name: /erin@example\.com/ })).toBeVisible();

	// Once he is a member, the invites and their Revoke action go away with the other admin controls
	await bob.getByRole('button', { name: 'Role of Bob Builder', exact: true }).click();
	await bob.getByRole('option', { name: 'Member' }).click();
	await expect(bob.getByText('Bob Builder is now member')).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Invite member' })).toBeHidden();
	await expect(bob.getByRole('heading', { name: 'Pending invites' })).toBeHidden();
	await expect(bob.getByRole('button', { name: 'Actions for erin@example.com' })).toHaveCount(0);
});
