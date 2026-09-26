import {
	expect,
	test,
	type APIRequestContext,
	type APIResponse,
	type Page
} from '@playwright/test';
import authUtil, { accounts } from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { fieldError } from '../utils/form.util';
import runUtil from '../utils/run.util';
import { joinWorkspace } from '../utils/workspace.util';

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Expects a refused request with the status and the error body the API answers with
async function expectError(
	response: APIResponse,
	status: number,
	error: { code: string; message?: string }
) {
	expect(response.status()).toBe(status);
	expect(await response.json()).toMatchObject(error);
}

// The ID of the user the page is signed in as
async function userId(page: Page) {
	const response = await page.request.get('/api/users/me');
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { id: string }).id;
}

// Changes a member's role through the owner's session, for tests where the members page isn't what's under test
async function setRole(request: APIRequestContext, memberId: string, role: 'admin' | 'member') {
	const response = await request.patch(`/api/workspace/members/${memberId}`, { data: { role } });
	expect(response.ok()).toBeTruthy();
}

// The accessible name the date picker gives a day, such as 'Tuesday, September 29, 2026'
function calendarDayName(date: Date) {
	return date.toLocaleDateString('en-US', {
		weekday: 'long',
		month: 'long',
		day: 'numeric',
		year: 'numeric'
	});
}

// What the API answers for a token that is unknown, deleted, expired or whose creator lost access
const invalidToken = { code: 'invalid_token', message: 'Token is invalid or expired' };

// What the API answers when a token calls something that needs a signed-in session
const sessionOnly = {
	code: 'forbidden',
	message: 'This can only be done while signed in, not with an API token'
};

// What the API answers a member who tries something only admins may do
const adminsOnly = { code: 'forbidden', message: 'Only admins of the workspace can do this' };

test('An API token authenticates scripts with a Bearer header, cannot mint tokens, records its use and stops working once deleted', async ({
	page,
	playwright,
	baseURL
}) => {
	// Every create request the page sends, to tell a refusal by the dialog from one by the server
	const creates: unknown[] = [];
	page.on('request', (request) => {
		if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/tokens') {
			creates.push(request.postDataJSON());
		}
	});

	// A name made only of spaces is refused by the dialog, which the recorded requests below prove
	await page.goto('/settings/tokens');
	await page.getByRole('button', { name: 'Create API token' }).click();
	const dialog = page.getByRole('dialog', { name: 'Create API token' });
	const name = dialog.getByLabel('Name', { exact: true });
	await name.fill('   ');
	await dialog.getByRole('button', { name: 'Create API token' }).click();

	// A real name creates the token, whose value is shown once, and the blank one never reached the server
	await name.fill('CI pipeline');
	await dialog.getByRole('button', { name: 'Create API token' }).click();
	const created = page.getByRole('dialog', { name: 'API token created' });
	const token = await created.getByRole('textbox', { name: 'CI pipeline' }).inputValue();
	expect(token).toMatch(/^ump_/);
	await created.getByRole('button', { name: 'Done' }).click();
	expect(creates).toEqual([{ name: 'CI pipeline' }]);

	// The new token was never used and never expires
	const row = page.getByRole('table', { name: 'API tokens' }).getByRole('row', {
		name: /CI pipeline/
	});
	await expect(row).toContainText('E2E User');
	await expect(row.getByText('Never', { exact: true })).toHaveCount(2);

	const api = await authUtil.bearerContext(playwright, baseURL, token);
	try {
		// The token acts in the workspace with its creator's role, and says it is a token
		const me = await api.get('/api/users/me');
		expect(me.status()).toBe(200);
		expect(await me.json()).toMatchObject({
			viaToken: true,
			workspace: { name: 'Default', role: 'owner' }
		});

		// A token can't mint a token that outlives it, nor revoke one
		await expectError(
			await api.post('/api/tokens', { data: { name: 'Minted' } }),
			403,
			sessionOnly
		);
		const listed = (await (await page.request.get('/api/tokens')).json()) as {
			items: { id: string; name: string }[];
		};
		expect(listed.items.map((t) => t.name)).toEqual(['CI pipeline']);
		await expectError(await api.delete(`/api/tokens/${listed.items[0].id}`), 403, sessionOnly);

		// The table now knows when the token was last used, while it still never expires
		await page.goto('/settings/tokens');
		await expect(row.getByText('Never', { exact: true })).toHaveCount(1);

		// The token is deleted through its row's menu
		await row.getByRole('button', { name: 'Actions for CI pipeline' }).click();
		await page.getByRole('menuitem', { name: 'Delete' }).click();
		await page
			.getByRole('alertdialog', { name: 'Delete CI pipeline' })
			.getByRole('button', { name: 'Delete' })
			.click();
		await expect(page.getByText('Deleted "CI pipeline"')).toBeVisible();
		await expect(row).toBeHidden();

		// The deleted token is refused right away
		await expectError(await api.get('/api/jobs'), 401, invalidToken);
	} finally {
		await api.dispose();
	}

	// A Bearer header wins over the session cookie sent along, so a deleted token never falls back to the session
	await expectError(
		await page.request.get('/api/jobs', { headers: { Authorization: `Bearer ${token}` } }),
		401,
		invalidToken
	);

	// A token that never existed is refused the same way
	const unknown = await authUtil.bearerContext(playwright, baseURL, 'ump_not-a-real-token');
	try {
		await expectError(await unknown.get('/api/jobs'), 401, invalidToken);
	} finally {
		await unknown.dispose();
	}
});

test('A job a script creates and runs with an API token records the run as triggered via the API', async ({
	page,
	playwright,
	baseURL
}) => {
	// The run uses a real sandbox, so this test gets more time than the suite default
	test.setTimeout(60_000);

	const { token } = await authUtil.createToken(page.request, 'CI pipeline');
	const api = await authUtil.bearerContext(playwright, baseURL, token);
	try {
		// The script creates a job and starts a run of it
		const job = await api.post('/api/jobs', {
			data: { name: 'Token job', instruction: 'Say hi', selfImprove: false }
		});
		expect(job.status()).toBe(200);
		const jobId = ((await job.json()) as { id: string }).id;
		await runUtil.scriptModel(page.request, [runUtil.finish('Done via API')]);
		const started = await api.post(`/api/jobs/${jobId}/runs`, { data: {} });
		expect(started.status()).toBe(200);
		const { runId } = (await started.json()) as { runId: string };

		// The run succeeds and says where it came from
		expect(await runUtil.waitForRun(page.request, runId)).toBe('succeeded');
		expect((await runUtil.getRun(page.request, runId)).trigger).toBe('api');
		await page.goto(`/runs/${runId}`);
		await expect(page.getByRole('heading', { level: 1 })).toContainText('Token job');
		await expect(page.getByText('Triggered via the API')).toBeVisible();
	} finally {
		await api.dispose();
	}
});

test("Members see and revoke only their own API tokens, while admins see everyone's with its creator", async ({
	page,
	browser
}, testInfo) => {
	// The owner has a token, and Bob joins as a member
	const ownerToken = await authUtil.createToken(page.request, 'Owner token');
	const bobPage = await joinWorkspace(browser, testInfo, page, accounts.bob);
	try {
		// Bob, a member, only sees his own token, without the column that says whose a token is
		await authUtil.createToken(bobPage.request, "Bob's script");
		await bobPage.goto('/settings/tokens');
		const bobTable = bobPage.getByRole('table', { name: 'API tokens' });
		await expect(bobTable.getByRole('row', { name: /Bob's script/ })).toBeVisible();
		await expect(bobTable.getByRole('row', { name: /Owner token/ })).toHaveCount(0);
		await expect(bobTable.getByRole('columnheader', { name: 'Created by' })).toHaveCount(0);

		// Nor can he revoke the owner's token, which to him doesn't exist
		const revoked = await bobPage.request.delete(`/api/tokens/${ownerToken.apiToken.id}`);
		expect(revoked.status()).toBe(404);
	} finally {
		await bobPage.context().close();
	}

	// The owner sees every token of the workspace and who created each
	await page.goto('/settings/tokens');
	const ownerTable = page.getByRole('table', { name: 'API tokens' });
	await expect(ownerTable.getByRole('columnheader', { name: 'Created by' })).toBeVisible();
	await expect(ownerTable.getByRole('row', { name: /Bob's script/ })).toContainText('Bob Builder');
	await expect(ownerTable.getByRole('row', { name: /Owner token/ })).toContainText('E2E User');
});

test("An API token acts with its creator's current role and can never change who has access", async ({
	page,
	browser,
	playwright,
	baseURL
}, testInfo) => {
	// Bob joins as a member and makes a token, after which only the token acts for him
	const bobPage = await joinWorkspace(browser, testInfo, page, accounts.bob);
	const bobId = await userId(bobPage);
	const { token } = await authUtil.createToken(bobPage.request, "Bob's script");
	await bobPage.context().close();

	const api = await authUtil.bearerContext(playwright, baseURL, token);
	try {
		// As a member's token it may use the workspace but not change its settings
		expect(await (await api.get('/api/users/me')).json()).toMatchObject({
			viaToken: true,
			workspace: { role: 'member' }
		});
		await expectError(
			await api.patch('/api/settings', { data: { retentionDays: 45 } }),
			403,
			adminsOnly
		);
		const job = await api.post('/api/jobs', {
			data: { name: 'Bob job', instruction: 'Say hi', selfImprove: false }
		});
		expect(job.status()).toBe(200);

		// Once Bob is an admin the same token is one too, without being created again
		await setRole(page.request, bobId, 'admin');
		expect(await (await api.get('/api/users/me')).json()).toMatchObject({
			workspace: { role: 'admin' }
		});
		expect((await api.patch('/api/settings', { data: { retentionDays: 45 } })).status()).toBe(200);
		const settings = (await (await page.request.get('/api/settings')).json()) as {
			retentionDays: number;
		};
		expect(settings.retentionDays).toBe(45);

		// Even an admin's token can't change who has access, so a leaked one can't grant itself lasting access
		await expectError(
			await api.post('/api/workspace/invites', { data: { role: 'admin', expiresInDays: 7 } }),
			403,
			sessionOnly
		);
		await expectError(
			await api.patch(`/api/workspace/members/${bobId}`, { data: { role: 'member' } }),
			403,
			sessionOnly
		);
		await expectError(
			await api.post('/api/workspaces', { data: { name: 'Token made' } }),
			403,
			sessionOnly
		);
		await expectError(await api.post('/api/workspace/leave'), 403, sessionOnly);

		// Neither the role change nor the leave went through, so Bob is still an admin of the workspace
		expect(await (await api.get('/api/users/me')).json()).toMatchObject({
			workspace: { role: 'admin' }
		});

		// Demoted again, the token loses the admin rights at once but keeps working as a member's
		await setRole(page.request, bobId, 'member');
		expect(await (await api.get('/api/users/me')).json()).toMatchObject({
			workspace: { role: 'member' }
		});
		await expectError(
			await api.patch('/api/settings', { data: { retentionDays: 5 } }),
			403,
			adminsOnly
		);
		expect((await api.get('/api/jobs')).status()).toBe(200);
	} finally {
		await api.dispose();
	}
});

test('An expired API token is refused and marked Expired, and a token cannot be created already expired', async ({
	page,
	playwright,
	baseURL
}) => {
	// The test waits for a token to expire in real time, which the poll below bounds before this timeout does
	test.setTimeout(30_000);

	// The server's own clock sets every expiry here, so a clock that differs on the machine running the tests can't make the token expire early or late
	const forever = await authUtil.createToken(page.request, 'Forever');
	const now = forever.apiToken.createdAt;

	// The API refuses an expiry in the past with a field error on expiresAt
	const past = await page.request.post('/api/tokens', {
		data: { name: 'Past', expiresAt: now - 60_000 }
	});
	expect(past.status()).toBe(400);
	expect(await past.json()).toMatchObject({
		code: 'validation_failed',
		fields: [{ field: 'expiresAt', message: 'must be in the future' }]
	});

	// A token that expires in a few seconds works until then and is refused afterwards
	const shortLived = await authUtil.createToken(page.request, 'Short-lived', now + 3_000);
	const api = await authUtil.bearerContext(playwright, baseURL, shortLived.token);
	try {
		expect((await api.get('/api/jobs')).status()).toBe(200);
		await expect
			.poll(async () => (await api.get('/api/jobs')).status(), {
				timeout: 10_000,
				intervals: [500]
			})
			.toBe(401);
		await expectError(await api.get('/api/jobs'), 401, invalidToken);
	} finally {
		await api.dispose();
	}

	// The table marks the expired token, next to one that never expires and was never used
	await page.goto('/settings/tokens');
	const table = page.getByRole('table', { name: 'API tokens' });
	await expect(table.getByRole('row', { name: /Short-lived/ })).toContainText('Expired');
	await expect(
		table.getByRole('row', { name: /Forever/ }).getByText('Never', { exact: true })
	).toHaveCount(2);
	await expect(table.getByRole('row', { name: /Past/ })).toHaveCount(0);
});

test('A token given an expiry date in the dialog expires at the start of that day, and the picker offers no day before tomorrow', async ({
	page
}) => {
	// The browser's clock stands in the middle of next month, so the calendar opens on a month that holds today and the picked day, and the picked day is in the future for the server too
	const today = new Date();
	today.setDate(1);
	today.setMonth(today.getMonth() + 1);
	today.setDate(15);
	today.setHours(12, 0, 0, 0);
	await page.clock.setFixedTime(today);
	const expiry = new Date(today);
	expiry.setDate(expiry.getDate() + 3);
	expiry.setHours(0, 0, 0, 0);

	// Today can't be picked, a day three days on can
	await page.goto('/settings/tokens');
	await page.getByRole('button', { name: 'Create API token' }).click();
	const dialog = page.getByRole('dialog', { name: 'Create API token' });
	await dialog.getByLabel('Name', { exact: true }).fill('Three days');
	await dialog.getByLabel('Expires').click();
	await expect(
		page.getByRole('button', { name: calendarDayName(today), exact: true })
	).toBeDisabled();
	await page.getByRole('button', { name: calendarDayName(expiry), exact: true }).click();
	await dialog.getByRole('button', { name: 'Create API token' }).click();
	await page
		.getByRole('dialog', { name: 'API token created' })
		.getByRole('button', { name: 'Done' })
		.click();

	// The token expires at local midnight of the picked day
	const listed = (await (await page.request.get('/api/tokens?search=Three')).json()) as {
		items: { name: string; expiresAt: number | null }[];
	};
	expect(listed.items).toEqual([
		expect.objectContaining({ name: 'Three days', expiresAt: expiry.getTime() })
	]);

	// Its row shows when it expires rather than Never, and only Last used still reads Never
	const row = page.getByRole('table', { name: 'API tokens' }).getByRole('row', {
		name: /Three days/
	});
	await expect(row.getByText('Never', { exact: true })).toHaveCount(1);
	await expect(row).not.toContainText('Expired');
});

test('A token name made only of spaces is refused next to the Name field', async ({ page }) => {
	await page.goto('/settings/tokens');
	await page.getByRole('button', { name: 'Create API token' }).click();
	const dialog = page.getByRole('dialog', { name: 'Create API token' });
	const name = dialog.getByLabel('Name', { exact: true });
	await name.fill('   ');
	await dialog.getByRole('button', { name: 'Create API token' }).click();
	await expect(fieldError(dialog, 'Name')).toHaveText('Required');
	await expect(name).toHaveAttribute('aria-invalid', 'true');
});
