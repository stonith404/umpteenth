import { expect, test as base, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { startFakeOAuthMcp, type FakeOAuthMcp } from '../utils/oauth-mcp.util';
import runUtil from '../utils/run.util';

// Every test gets its own fake MCP server with its authorization server
const test = base.extend<{ fake: FakeOAuthMcp }>({
	fake: async ({}, use) => {
		const fake = await startFakeOAuthMcp();
		await use(fake);
		await fake.close();
	}
});

test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// Adds an HTTP server through the dialog, optionally with headers
async function addHttpServer(page: Page, url: string, headers: Record<string, string> = {}) {
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill('issues');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill(url);
	for (const [i, [key, value]] of Object.entries(headers).entries()) {
		await dialog.getByRole('button', { name: 'Add header' }).click();
		await dialog.getByLabel(`Header ${i + 1} name`).fill(key);
		await dialog.getByLabel(`Header ${i + 1} value`).fill(value);
	}
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
}

// Allows the pending login on the fake's consent page and waits for the test that follows it
async function allowLogin(page: Page) {
	await expect(page.getByRole('heading', { name: 'Fake Issues' })).toBeVisible();
	await page.getByRole('button', { name: 'Allow' }).click();
	await expect(page.getByText('Logged in to "issues"')).toBeVisible();
	const testDialog = page.getByRole('dialog', { name: 'Test issues' });
	await expect(testDialog.getByText('Connected')).toBeVisible();
	return testDialog;
}

function serverRow(page: Page) {
	return page.getByRole('table', { name: 'MCP servers' }).getByRole('row', { name: /issues/ });
}

test('Adding a server that needs an OAuth login opens the login right away', async ({
	page,
	fake
}) => {
	await addHttpServer(page, fake.mcpUrl);

	// The login was detected and started with a registered client, PKCE, the resource and offline_access for a refresh token
	await expect(page.getByRole('heading', { name: 'Fake Issues' })).toBeVisible();
	await expect(page.getByText('Umpteenth wants to access your issues.')).toBeVisible();
	expect(fake.authorizations).toHaveLength(1);
	const [authorization] = fake.authorizations;
	expect(authorization).toMatchObject({
		scope: 'issues:read offline_access',
		resource: fake.mcpUrl,
		codeChallengeMethod: 'S256'
	});
	expect(authorization.redirectUri).toMatch(/\/api\/mcp-servers\/[^/]+\/oauth\/callback$/);
	expect(fake.clients.get(authorization.clientId)?.name).toBe('Umpteenth');

	// Allowing it comes back to the MCP servers page, which tests the server with the new login
	const testDialog = await allowLogin(page);
	await expect(page).toHaveURL('/mcp');
	await expect(testDialog.getByText('list_issues')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(testDialog).toBeHidden();
	await expect(serverRow(page)).toContainText('OAuth');

	// The detail sheet shows the login and offers to end it
	await serverRow(page).getByRole('button', { name: 'issues', exact: true }).click();
	const sheet = page.getByRole('dialog', { name: /issues/ });
	await expect(sheet.getByText('Access tokens are refreshed automatically.')).toBeVisible();
	await expect(sheet.getByRole('button', { name: 'Log out' })).toBeVisible();
});

test('A job run calls the tools of a server it reaches through the OAuth login', async ({
	page,
	fake
}) => {
	// Access tokens this short-lived are refreshed before every use, so the run also exercises the refresh token rotation
	fake.accessTokenTtl = 5;
	await addHttpServer(page, fake.mcpUrl);
	await allowLogin(page);
	const refreshesBeforeRun = fake.refreshes;
	expect(refreshesBeforeRun).toBeGreaterThan(0);

	// Attach the server to a job whose run calls its tool
	const servers = (await (await page.request.get('/api/mcp-servers')).json()) as {
		items: { id: string }[];
	};
	const job = await runUtil.createJob(page.request, 'Issue digest');
	const attached = await page.request.put(`/api/jobs/${job.id}/mcp-servers`, {
		data: [{ serverId: servers.items[0].id, allowedTools: null }]
	});
	expect(attached.ok()).toBeTruthy();
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: 'issues__list_issues', args: {} }] },
		{ toolCalls: [{ name: 'finish', args: { status: 'success', summary: 'One open issue' } }] }
	]);
	expect(status).toBe('succeeded');

	// The call reached the server with a freshly refreshed access token
	expect(fake.toolCalls).toHaveLength(1);
	expect(fake.toolCalls[0].authorization).toMatch(/^Bearer access-\d+$/);
	expect(fake.refreshes).toBeGreaterThan(refreshesBeforeRun);

	// The timeline shows what the tool returned
	await page.goto(`/runs/${runId}`);
	await expect(
		page.getByTestId('run-timeline').getByRole('region', { name: 'Result of issues__list_issues' })
	).toContainText('#1 The login button is blue');
});

test('A provider that rejects the advertised scopes is asked again without scopes', async ({
	page,
	fake
}) => {
	fake.rejectScopes = true;
	await addHttpServer(page, fake.mcpUrl);

	// The first request is refused with invalid_scope, so the login starts over without scopes, like Codex does
	await expect(page.getByRole('heading', { name: 'Fake Issues' })).toBeVisible();
	await expect(page.getByText('Scopes: none')).toBeVisible();
	expect(fake.authorizations.map((a) => a.scope)).toEqual(['issues:read offline_access', null]);
	await allowLogin(page);
});

test('Logging out and refusing a login leave the server asking for a login', async ({
	page,
	fake
}) => {
	await addHttpServer(page, fake.mcpUrl);
	const testDialog = await allowLogin(page);
	await page.keyboard.press('Escape');
	await expect(testDialog).toBeHidden();

	// Log out from the server's detail sheet
	await serverRow(page).getByRole('button', { name: 'issues', exact: true }).click();
	await page
		.getByRole('dialog', { name: /issues/ })
		.getByRole('button', { name: 'Log out' })
		.click();
	await page.getByRole('alertdialog').getByRole('button', { name: 'Log out' }).click();
	await expect(page.getByText('Logged out of "issues"')).toBeVisible();
	await expect(serverRow(page)).toContainText('Not logged in');

	// Refusing the next login reports the provider's answer and keeps the server logged out
	await serverRow(page).getByRole('button', { name: 'Log in' }).click();
	await expect(page.getByRole('heading', { name: 'Fake Issues' })).toBeVisible();
	await page.getByRole('button', { name: 'Deny' }).click();
	await expect(page.getByText('Failed to log in to "issues"')).toBeVisible();
	await expect(page.getByText(/access_denied/)).toBeVisible();
	await expect(page).toHaveURL('/mcp');
	await expect(serverRow(page)).toContainText('Not logged in');
});

test('A revoked login makes the test ask for a new login', async ({ page, fake }) => {
	fake.accessTokenTtl = 5;
	await addHttpServer(page, fake.mcpUrl);
	const testDialog = await allowLogin(page);

	// The user revoked the app at the provider, so the next refresh fails and the login ends
	fake.revokeRefreshTokens();
	await testDialog.getByRole('button', { name: 'Test again' }).click();
	await expect(testDialog.getByText('Login required')).toBeVisible();

	// The dialog starts the new login
	await testDialog.getByRole('button', { name: 'Log in' }).click();
	await allowLogin(page);
});

test('A server with an Authorization header uses it instead of an OAuth login', async ({
	page,
	fake
}) => {
	await addHttpServer(page, fake.mcpUrl, { Authorization: 'Bearer static-token' });

	// The header counts as a bearer token like in Codex, so no login opens
	await expect(page.getByText('Added "issues"')).toBeVisible();
	const testDialog = page.getByRole('dialog', { name: 'Test issues' });
	await expect(testDialog).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(testDialog).toBeHidden();
	await expect(serverRow(page)).toContainText('Bearer token');
	expect(fake.authorizations).toHaveLength(0);
});
