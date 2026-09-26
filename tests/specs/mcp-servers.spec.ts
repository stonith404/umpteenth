import { expect, test as base, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import {
	attachMcpServers,
	createMcpServer,
	jobMcpServers,
	notesTools,
	requireApiKey,
	startFakeMcp,
	testMcpServer,
	unreachableMcpUrl,
	type FakeMcp,
	type McpServer
} from '../utils/mcp.util';
import { timelineStep } from '../utils/run-view.util';
import runUtil from '../utils/run.util';
import { createSecret } from '../utils/workspace.util';

// Every test gets its own notes server, which is closed however the test ends
const test = base.extend<{ fake: FakeMcp }>({
	fake: async ({}, use) => {
		const fake = await startFakeMcp(notesTools());
		await use(fake);

		// Closing stops the fake from listening right away, but its promise waits for idle connections the backend's HTTP client may keep open for a minute
		void fake.close();
	}
});

// Some tests start runs in real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The server as GET /api/mcp-servers/{id} returns it
async function getMcpServer(request: APIRequestContext, id: string) {
	const response = await request.get(`/api/mcp-servers/${id}`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as McpServer;
}

// The workspace's MCP servers
async function listMcpServers(request: APIRequestContext) {
	const response = await request.get('/api/mcp-servers');
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as { total: number; items: McpServer[] };
}

// Adds an HTTP server through the dialog on the MCP servers page, which tests it right away when it doesn't need a login
async function addHttpServer(
	page: Page,
	{ name, url, description }: { name: string; url: string; description?: string }
) {
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill(name);
	if (description) {
		await dialog.getByPlaceholder('What the server gives access to').fill(description);
	}
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill(url);
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
	await expect(page.getByText(`Added "${name}"`)).toBeVisible();
}

// The server's row in the table on the MCP servers page
function serverRow(page: Page, name: string) {
	return page
		.getByRole('table', { name: 'MCP servers' })
		.getByRole('row', { name: new RegExp(`\\b${name}\\b`) });
}

// Collects the URL and body of every response whose URL contains the given part, for checking what reached the browser
// A body the browser no longer holds is null, so a check can tell it apart from an empty one
function collectResponseBodies(page: Page, urlPart: string) {
	const bodies: Promise<{ url: string; body: string | null }>[] = [];
	page.on('response', (response) => {
		if (!response.url().includes(urlPart)) return;
		bodies.push(
			response.text().then(
				(body) => ({ url: response.url(), body }),
				() => ({ url: response.url(), body: null })
			)
		);
	});
	return () => Promise.all(bodies);
}

test('A plain HTTP server is added, tested right away and lists its tools with their read-only and destructive annotations', async ({
	page,
	fake
}) => {
	// Without any server the only add button is the one in the empty state
	await page.goto('/mcp');
	await expect(page.getByText('No MCP servers yet')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Add MCP server' })).toHaveCount(1);

	// Fill in the dialog, whose OAuth client section can't show a callback URL before the server exists
	await page.getByRole('button', { name: 'Add MCP server' }).click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill('notes');
	await dialog.getByPlaceholder('What the server gives access to').fill('Team notes');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill(fake.url);
	await dialog.getByRole('button', { name: 'OAuth client' }).click();
	await expect(dialog.getByLabel('Client ID')).toBeVisible();
	await expect(dialog).not.toContainText('/oauth/callback');
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
	await expect(page.getByText('Added "notes"')).toBeVisible();

	// The server needs no login, so the test starts on its own and finds the three tools
	const testDialog = page.getByRole('dialog', { name: 'Test notes' });
	await expect(testDialog.getByText('Connected')).toBeVisible();
	await expect(testDialog).toContainText(/found 3\s+tools\./);
	const tools = testDialog.getByRole('list', { name: 'Tools' }).getByRole('listitem');
	await expect(tools).toHaveCount(3);

	// The annotations show as badges, and a tool without parameters has no schema to expand
	const listNotes = tools.filter({ hasText: 'list_notes' });
	await expect(listNotes).toContainText('Read-only');
	await expect(listNotes).not.toContainText('Destructive');
	await expect(listNotes.getByRole('button')).toBeDisabled();
	const addNote = tools.filter({ hasText: 'add_note' });
	await expect(addNote).not.toContainText('Read-only');
	await expect(addNote).not.toContainText('Destructive');
	const deleteNote = tools.filter({ hasText: 'delete_note' });
	await expect(deleteNote).toContainText('Destructive');
	await expect(deleteNote).not.toContainText('Read-only');

	// A tool with parameters expands to its input schema
	const schema = testDialog.getByRole('textbox', { name: 'Input schema of delete_note' });
	await expect(schema).toBeHidden();
	await deleteNote.getByRole('button').click();
	await expect(schema).toBeVisible();
	await expect(schema).toContainText('"id"');
	await page.keyboard.press('Escape');
	await expect(page.getByRole('dialog')).toHaveCount(0);

	// The row shows no authentication, the tool count of the test and the description
	const row = serverRow(page, 'notes');
	await expect(row).toContainText('None');
	await expect(row).toContainText('3 tools');
	await expect(row).toContainText('Team notes');

	// The test cached the tools with their annotations, and the server counts as one without a login
	const [server] = (await listMcpServers(page.request)).items;
	expect(server).toMatchObject({
		name: 'notes',
		description: 'Team notes',
		transport: 'http',
		url: fake.url,
		command: null,
		enabled: true,
		auth: { status: 'unsupported' },
		tools: [
			{ name: 'list_notes', readOnly: true, destructive: false },
			{ name: 'add_note', readOnly: false, destructive: false },
			{ name: 'delete_note', readOnly: false, destructive: true }
		]
	});
	expect(server.toolsCachedAt).toEqual(expect.any(Number));
});

test('A server without an OAuth login offers none, and editing it from its sheet saves without testing again', async ({
	page,
	fake
}) => {
	// A tested notes server without any login
	const { id } = await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: fake.url,
		description: 'Team notes'
	});
	await testMcpServer(page.request, id);

	// The row's menu has nothing to log in or out of
	await page.goto('/mcp');
	const row = serverRow(page, 'notes');
	await row.getByRole('button', { name: 'Actions for notes' }).click();
	await expect(page.getByRole('menuitem', { name: 'Edit' })).toBeVisible();
	await expect(page.getByRole('menuitem', { name: 'Delete' })).toBeVisible();
	await expect(page.getByRole('menuitem', { name: /Log in|Log out/ })).toHaveCount(0);
	await page.keyboard.press('Escape');

	// The API refuses a login for it too
	const login = await page.request.post(`/api/mcp-servers/${id}/oauth/login`);
	expect(login.status()).toBe(422);
	expect(await login.json()).toMatchObject({
		code: 'unsupported',
		message: "This server doesn't advertise an OAuth login"
	});

	// The sheet shows the endpoint, the missing login and the tools of the last test
	await row.getByRole('button', { name: 'notes', exact: true }).click();
	const sheet = page.getByRole('dialog', { name: /notes/ });
	await expect(sheet.getByText('HTTP', { exact: true })).toBeVisible();
	await expect(sheet).toContainText(fake.url);
	await expect(sheet.getByText(/doesn't advertise an OAuth login/)).toBeVisible();
	await expect(sheet.getByRole('button', { name: /Log in|Log out/ })).toHaveCount(0);
	await expect(sheet.getByText(/^Tested/)).toBeVisible();
	await expect(sheet.getByRole('list', { name: 'Tools' }).getByRole('listitem')).toHaveCount(3);

	// The edit dialog starts from the saved server and shows the callback URL an OAuth client would need
	await sheet.getByRole('button', { name: 'Edit' }).click();
	const dialog = page.getByRole('dialog', { name: 'Edit notes' });
	await expect(dialog.getByLabel('Name')).toHaveValue('notes');
	await expect(dialog.getByLabel('URL')).toHaveValue(fake.url);
	await dialog.getByRole('button', { name: 'OAuth client' }).click();
	await expect(dialog).toContainText(`/api/mcp-servers/${id}/oauth/callback`);

	// Saving a new description updates the row, and only new servers are tested on their own
	await dialog.getByPlaceholder('What the server gives access to').fill('Shared team notes');
	await dialog.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Saved "notes"')).toBeVisible();
	await expect(row).toContainText('Shared team notes');
	await expect(page.getByRole('dialog')).toHaveCount(0);
	expect(await getMcpServer(page.request, id)).toMatchObject({
		description: 'Shared team notes',
		url: fake.url,
		auth: { status: 'unsupported' }
	});
});

test('The server search matches descriptions too', async ({ page, fake }) => {
	// Only the notes server mentions sharing, and only in its description
	await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: fake.url,
		description: 'Shared team notes'
	});
	await createMcpServer(page.request, {
		name: 'github',
		transport: 'stdio',
		command: 'npx'
	});
	await page.goto('/mcp');
	const notes = serverRow(page, 'notes');
	const github = serverRow(page, 'github');
	await expect(github).toBeVisible();

	// The search reaches the URL after its debounce, and the table keeps its old rows until the backend answers, so the other server leaving marks the answer
	await page.getByRole('searchbox', { name: 'Search servers' }).fill('shared');
	await expect(page).toHaveURL(/search=shared/);
	await expect(github).toBeHidden();
	await expect(notes).toBeVisible();
});

test('The add dialog checks the name, the command, the URL and the OAuth client before it saves anything', async ({
	page
}) => {
	// Open the dialog on the empty page
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	const name = dialog.getByLabel('Name');
	const submit = dialog.getByRole('button', { name: 'Add MCP server' });

	// An empty stdio server lacks its name and its command
	await submit.click();
	await expect(dialog.getByText('Required', { exact: true })).toBeVisible();
	await expect(dialog.getByText('Required for stdio servers')).toBeVisible();
	await expect(name).toHaveAttribute('aria-invalid', 'true');

	// Names become part of tool names, so spaces are refused
	await name.fill('team notes');
	await submit.click();
	await expect(dialog.getByText('Only letters, digits, dashes and underscores')).toBeVisible();

	// An HTTP server needs a URL, and the command typed for stdio doesn't count
	await name.fill('notes');
	await dialog.getByLabel('Command').fill('npx');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await submit.click();
	await expect(dialog.getByText('Required for HTTP servers')).toBeVisible();
	await expect(name).toHaveAttribute('aria-invalid', 'false');

	// Only http and https URLs are allowed
	await dialog.getByLabel('URL').fill('ftp://example.com/mcp');
	await submit.click();
	await expect(dialog.getByText('Must start with http:// or https://')).toBeVisible();

	// A client secret needs the client it belongs to
	await dialog.getByLabel('URL').fill('https://mcp.example.com/mcp');
	await dialog.getByRole('button', { name: 'OAuth client' }).click();
	await dialog.getByLabel('Client secret').fill('shh');
	await submit.click();
	await expect(dialog.getByText('Needs a client ID')).toBeVisible();

	// Nothing was saved along the way
	expect((await listMcpServers(page.request)).total).toBe(0);
});

test('Only the fields of the chosen transport are saved', async ({ page, fake }) => {
	// Start as a stdio server with an argument and an environment variable, then switch to HTTP
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill('notes');
	await dialog.getByLabel('Command').fill('npx');
	await dialog.getByRole('button', { name: 'Add argument' }).click();
	await dialog.getByLabel('Argument 1', { exact: true }).fill('-y');
	await dialog.getByRole('button', { name: 'Add variable' }).click();
	await dialog.getByLabel('Environment variable 1 name').fill('NOTES_TOKEN');
	await dialog.getByLabel('Environment variable 1 value').fill('plain-token');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill(fake.url);

	// Adding the server tests it right away, and the test is awaited so it can't run into the next test's cleanup
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
	await expect(page.getByText('Added "notes"')).toBeVisible();
	await expect(
		page.getByRole('dialog', { name: 'Test notes' }).getByText('Connected')
	).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(page.getByRole('dialog')).toHaveCount(0);

	// The stdio command, argument and variable were dropped, and no OAuth client was stored
	const servers = await listMcpServers(page.request);
	expect(servers.total).toBe(1);
	const [server] = servers.items;
	expect(server).toMatchObject({
		name: 'notes',
		transport: 'http',
		url: fake.url,
		command: null,
		args: []
	});
	expect(server.env).toEqual({});
	expect(server.oauth).toEqual({});
});

test("The add dialog stays open with the backend's reason when it refuses a taken name or a scope", async ({
	page
}) => {
	// A notes server exists already
	await createMcpServer(page.request, {
		name: 'notes',
		transport: 'stdio',
		command: 'npx'
	});
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	const name = dialog.getByLabel('Name');
	const submit = dialog.getByRole('button', { name: 'Add MCP server' });

	// A second server with the same name is refused, which the name field shows
	await name.fill('notes');
	await dialog.getByLabel('Command').fill('npx');
	await submit.click();
	await expect(
		dialog.getByText('MCP server name is already in use', { exact: true })
	).toBeVisible();
	await expect(name).toHaveAttribute('aria-invalid', 'true');

	// A scope with a space is refused as well, and the dialog stays open with the reason
	await name.fill('wiki');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill('https://mcp.example.com/mcp');
	await dialog.getByRole('button', { name: 'OAuth client' }).click();
	await dialog.getByRole('button', { name: 'Add scope' }).click();
	await dialog.getByLabel('Scope 1', { exact: true }).fill('read write');
	await submit.click();
	await expect(page.getByText(/must not contain spaces/i).first()).toBeVisible();
	await expect(dialog).toBeVisible();
	expect((await listMcpServers(page.request)).total).toBe(1);
});

test('The API refuses invalid servers from clients that skip the dialog', async ({ page }) => {
	// A notes server exists already, so its name is taken
	await createMcpServer(page.request, {
		name: 'notes',
		transport: 'stdio',
		command: 'npx'
	});

	// A name with a space, a stdio server without a command, an ftp URL and the taken name
	const refusals = await Promise.all(
		[
			{ name: 'bad name', transport: 'stdio', command: 'npx' },
			{ name: 'x', transport: 'stdio' },
			{ name: 'y', transport: 'http', url: 'ftp://example.com/mcp' },
			{ name: 'notes', transport: 'stdio', command: 'npx' }
		].map(async (data) => {
			const response = await page.request.post('/api/mcp-servers', { data });
			const body = (await response.json()) as { code: string; fields?: { field: string }[] };
			return { status: response.status(), code: body.code, field: body.fields?.[0]?.field };
		})
	);
	expect(refusals).toEqual([
		{ status: 400, code: 'validation_failed', field: 'body.name' },
		{ status: 400, code: 'validation_failed', field: 'command' },
		{ status: 400, code: 'validation_failed', field: 'url' },
		{ status: 409, code: 'already_in_use', field: undefined }
	]);
});

test('A URL without a scheme is reported next to the URL field', async ({ page }) => {
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill('notes');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill('mcp.example.com/mcp');
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
	await expect(dialog.getByText('Must start with http:// or https://')).toBeVisible();
	await expect(dialog.getByLabel('URL')).toHaveAttribute('aria-invalid', 'true');
});

test('A scope the backend refuses is reported next to the scopes', async ({ page, fake }) => {
	await page.goto('/mcp');
	await page.getByRole('button', { name: 'Add MCP server' }).first().click();
	const dialog = page.getByRole('dialog', { name: 'Add MCP server' });
	await dialog.getByLabel('Name').fill('notes');
	await dialog.getByRole('tab', { name: 'HTTP' }).click();
	await dialog.getByLabel('URL').fill(fake.url);
	await dialog.getByRole('button', { name: 'OAuth client' }).click();
	await dialog.getByRole('button', { name: 'Add scope' }).click();
	await dialog.getByLabel('Scope 1', { exact: true }).fill('read write');
	await dialog.getByRole('button', { name: 'Add MCP server' }).click();
	await expect(dialog.getByText(/must not contain spaces/i)).toBeVisible();
});

test('Header values that reference a secret reach the server but never the browser, and follow the secret when it changes', async ({
	page,
	fake
}) => {
	// The server only lets the secret's value through, and every servers API body the page gets is kept for the leak check at the end
	const secretId = await createSecret(page.request, 'NOTES_KEY', 'key-one-7f3a');
	const gate = await requireApiKey(fake, 'key-one-7f3a');
	const responseBodies = collectResponseBodies(page, '/api/mcp-servers');

	try {
		// Without the key the server turns the test away, which the dialog explains
		await addHttpServer(page, { name: 'notes', url: gate.url });
		const testDialog = page.getByRole('dialog', { name: 'Test notes' });
		await expect(testDialog.getByText(/^Connection failed after/)).toBeVisible();
		await expect(testDialog.getByText(/requires authorization/)).toBeVisible();
		await expect(testDialog.getByRole('button', { name: 'Test again' })).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(page.getByRole('dialog')).toHaveCount(0);

		// Add the key as a header that references the secret, picked from the dialog's secret menu
		const row = serverRow(page, 'notes');
		await row.getByRole('button', { name: 'Actions for notes' }).click();
		await page.getByRole('menuitem', { name: 'Edit' }).click();
		const dialog = page.getByRole('dialog', { name: 'Edit notes' });
		await dialog.getByRole('button', { name: 'Add header' }).click();
		await dialog.getByLabel('Header 1 name').fill('X-Api-Key');
		await dialog.getByRole('button', { name: 'Insert a secret into header 1' }).click();
		await page.getByRole('menuitem', { name: 'NOTES_KEY' }).click();
		await expect(dialog.getByLabel('Header 1 value')).toHaveValue('{{secret:NOTES_KEY}}');
		await dialog.getByRole('button', { name: 'Save' }).click();
		await expect(page.getByText('Saved "notes"')).toBeVisible();

		// The host fills in the secret, so the test gets through
		await row.getByRole('button', { name: 'Test' }).click();
		await expect(testDialog.getByText('Connected')).toBeVisible();
		expect(gate.seenKeys[0]).toBeUndefined();
		expect(gate.seenKeys.at(-1)).toBe('key-one-7f3a');
		await page.keyboard.press('Escape');
		await expect(page.getByRole('dialog')).toHaveCount(0);

		// The sheet shows the reference, never the value, and the custom header is no bearer token
		await row.getByRole('button', { name: 'notes', exact: true }).click();
		const sheet = page.getByRole('dialog', { name: /notes/ });
		await expect(sheet.getByRole('heading', { name: 'Headers' })).toBeVisible();
		await expect(sheet).toContainText('X-Api-Key');
		await expect(sheet).toContainText('{{secret:NOTES_KEY}}');
		await expect(sheet).not.toContainText('key-one-7f3a');
		await expect(sheet.getByText('None', { exact: true })).toBeVisible();

		// A rotated secret reaches the server with the next test
		const rotated = await page.request.put(`/api/secrets/${secretId}`, {
			data: { value: 'key-two-91bc' }
		});
		expect(rotated.ok()).toBeTruthy();
		gate.key = 'key-two-91bc';
		await sheet.getByRole('button', { name: 'Test' }).click();
		await expect(testDialog.getByText('Connected')).toBeVisible();
		expect(gate.seenKeys.at(-1)).toBe('key-two-91bc');

		// A deleted secret stops the test before it connects, and the user is told which secret is missing
		const deleted = await page.request.delete(`/api/secrets/${secretId}`);
		expect(deleted.ok()).toBeTruthy();
		const seenBefore = gate.seenKeys.length;
		await testDialog.getByRole('button', { name: 'Test again' }).click();
		await expect(page.getByText(/Secret NOTES_KEY not found/).first()).toBeVisible();
		await expect(testDialog.getByText('Connected')).toBeHidden();
		expect(gate.seenKeys).toHaveLength(seenBefore);

		// The API keeps the reference, and none of the list or test responses the page read carried a key
		const [server] = (await listMcpServers(page.request)).items;
		expect(server.headers).toEqual({ 'X-Api-Key': '{{secret:NOTES_KEY}}' });
		expect(server.auth.status).toBe('unsupported');
		const bodies = (await responseBodies()).filter((r) => r.body !== null);
		expect(bodies.some((r) => new URL(r.url).pathname === '/api/mcp-servers')).toBeTruthy();
		expect(bodies.filter((r) => r.url.endsWith('/test')).length).toBeGreaterThanOrEqual(3);
		for (const { body } of bodies) {
			expect(body).not.toContain('key-one-7f3a');
			expect(body).not.toContain('key-two-91bc');
		}
	} finally {
		void gate.close();
	}
});

test('The compile step marks needed services as configured when a server matches, preselects it and saves the chosen servers', async ({
	page,
	fake
}) => {
	// A tested HTTP server that the compiled spec names in a different case, and an untested stdio server it doesn't name
	const notes = await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: fake.url
	});
	await testMcpServer(page.request, notes.id);
	const github = await createMcpServer(page.request, {
		name: 'github',
		transport: 'stdio',
		command: 'npx'
	});
	const needs = [
		{ server: 'Notes', why: 'read the team notes' },
		{ server: 'jira', why: 'file a ticket' }
	];
	await runUtil.scriptModel(page.request, [
		{
			text: JSON.stringify({
				title: 'Note digest',
				goal: 'Summarize the team notes',
				schedule: null,
				successCriteria: ['Every note is read'],
				inputs: [],
				outputs: [],
				mcp: needs,
				network: 'none',
				dockerfile: null,
				sideEffects: [],
				questions: []
			})
		}
	]);

	// Compile a description
	await page.goto('/jobs/new');
	await page
		.getByRole('textbox', { name: 'Describe the job' })
		.fill('Summarize the team notes and file a ticket for each open question.');
	await page.getByRole('button', { name: 'Compile' }).click();
	await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Note digest', {
		timeout: 15_000
	});

	// The service with a server of the same name is configured, the other one says how to add it
	const needed = page.getByRole('list', { name: 'Services the job needs' }).getByRole('listitem');
	await expect(needed).toHaveCount(2);
	await expect(needed.nth(0)).toContainText('Notes');
	await expect(needed.nth(0)).toContainText('Configured');
	await expect(needed.nth(0)).toContainText('Read the team notes');
	await expect(needed.nth(1)).toContainText('jira');
	await expect(needed.nth(1)).toContainText('Not configured');
	await expect(needed.nth(1).getByRole('link', { name: 'MCP servers' })).toHaveAttribute(
		'href',
		'/mcp'
	);

	// The matching server is preselected, and the other one can be attached as well
	const notesBox = page.getByRole('checkbox', { name: /^notes/ });
	const githubBox = page.getByRole('checkbox', { name: /^github/ });
	await expect(notesBox).toBeChecked();
	await expect(notesBox).toHaveAccessibleName(/3 tools/);
	await expect(githubBox).not.toBeChecked();
	await githubBox.check();

	// Saving creates the job with both servers attached, allowing all their tools
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page.getByText('Created "Note digest"')).toBeVisible();
	await expect(page).toHaveURL(/\/jobs\/[0-9a-f-]+$/);
	const jobId = page.url().split('/').pop()!;
	expect(await jobMcpServers(page.request, jobId)).toEqual(
		expect.arrayContaining([
			{ serverId: notes.id, serverName: 'notes', allowedTools: null },
			{ serverId: github.id, serverName: 'github', allowedTools: null }
		])
	);
	const job = (await (await page.request.get(`/api/jobs/${jobId}`)).json()) as {
		spec: { mcp: unknown };
	};
	expect(job.spec.mcp).toEqual(needs);
});

test('Disabling a server keeps its settings, marks it disabled everywhere and leaves it out of runs until it is enabled again', async ({
	page,
	fake
}) => {
	// A notes server with a custom header, attached to a job
	const server = await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: fake.url,
		description: 'Team notes',
		headers: { 'X-Team': 'blue' }
	});
	const job = await runUtil.createJob(page.request, 'Note digest');
	await attachMcpServers(page.request, job.id, [{ serverId: server.id, allowedTools: null }]);

	// The switch sends the whole server back, so nothing but the enabled state changes
	await page.goto('/mcp');
	const saved = page.waitForResponse(
		(r) => r.url().endsWith(`/api/mcp-servers/${server.id}`) && r.request().method() === 'PUT'
	);
	await page.getByRole('switch', { name: 'Disable notes' }).click();
	expect((await saved).ok()).toBeTruthy();
	await expect(page.getByRole('switch', { name: 'Enable notes' })).not.toBeChecked();
	expect(await getMcpServer(page.request, server.id)).toMatchObject({
		enabled: false,
		description: 'Team notes',
		url: fake.url,
		headers: { 'X-Team': 'blue' }
	});

	// The sheet marks it disabled and still lists its header
	await serverRow(page, 'notes').getByRole('button', { name: 'notes', exact: true }).click();
	const sheet = page.getByRole('dialog', { name: /notes/ });
	await expect(sheet.getByText('Disabled', { exact: true })).toBeVisible();
	await expect(sheet).toContainText('X-Team');
	await expect(sheet).toContainText('blue');
	await page.keyboard.press('Escape');

	// The job keeps the attachment, marked disabled
	await page.goto(`/jobs/${job.id}/settings`);
	const attached = page.getByRole('list', { name: 'Attached MCP servers' }).getByRole('listitem');
	await expect(attached).toHaveCount(1);
	await expect(attached).toContainText('notes');
	await expect(attached.getByText('Disabled', { exact: true })).toBeVisible();

	// A run skips the disabled server without connecting to it
	const requestsBefore = fake.requestHeaders.length;
	const disabledRun = await runUtil.runScripted(page.request, job.id, [runUtil.finish('Done')]);
	expect(disabledRun.status).toBe('succeeded');
	expect(fake.requestHeaders).toHaveLength(requestsBefore);
	expect(
		(await runUtil.getEvents(page.request, disabledRun.runId)).filter((e) => e.type === 'mcp.call')
	).toHaveLength(0);

	// Enabled again, the next run connects with the header it kept
	await page.goto('/mcp');
	await page.getByRole('switch', { name: 'Enable notes' }).click();
	await expect(page.getByRole('switch', { name: 'Disable notes' })).toBeChecked();
	await expect.poll(async () => (await getMcpServer(page.request, server.id)).enabled).toBe(true);
	const enabledRun = await runUtil.runScripted(page.request, job.id, [runUtil.finish('Done')]);
	expect(enabledRun.status).toBe('succeeded');
	expect(fake.requestHeaders.slice(requestsBefore).map((h) => h['x-team'])).toContain('blue');
	await page.goto(`/runs/${enabledRun.runId}`);
	await expect(timelineStep(page, 'Connected to notes')).toContainText('3 tools');
});

test('An unreachable server shows the connection error in its test, stays Unknown, refuses a login and is reported on the run timeline', async ({
	page
}) => {
	// Adding the server tests it right away, against a port nothing listens on
	await addHttpServer(page, { name: 'broken', url: await unreachableMcpUrl() });

	// The test fails with the reason, and so does the next one
	const testDialog = page.getByRole('dialog', { name: 'Test broken' });
	const failure = testDialog.getByText(/^Connection failed after/);
	await expect(failure).toBeVisible();
	await expect(testDialog).toContainText('failed to connect to broken');
	const retest = page.waitForResponse(
		(r) => r.url().endsWith('/test') && r.request().method() === 'POST'
	);
	await testDialog.getByRole('button', { name: 'Test again' }).click();
	expect(await (await retest).json()).toMatchObject({
		ok: false,
		error: expect.stringContaining('failed to connect to broken')
	});
	await expect(failure).toBeVisible();
	await expect(testDialog).toContainText('failed to connect to broken');
	await page.keyboard.press('Escape');
	await expect(page.getByRole('dialog')).toHaveCount(0);

	// OAuth detection couldn't reach the server, so its login state is unknown
	const row = serverRow(page, 'broken');
	await expect(row).toContainText('Unknown');

	// A login can't start without reaching the server, and the page stays where it is
	await row.getByRole('button', { name: 'broken', exact: true }).click();
	const sheet = page.getByRole('dialog', { name: /broken/ });
	await sheet.getByRole('button', { name: 'Log in' }).click();
	await expect(page.getByText('Failed to start the login for "broken"')).toBeVisible();
	await expect(page.getByText(/Failed to start the OAuth login/)).toBeVisible();
	await expect(page).toHaveURL('/mcp');
	const [server] = (await listMcpServers(page.request)).items;
	expect(server).toMatchObject({ auth: { status: 'unknown' }, toolsCachedAt: null, tools: [] });

	// A run with the server attached still finishes, and its timeline says why the server is missing
	const job = await runUtil.createJob(page.request, 'Broken digest');
	await attachMcpServers(page.request, job.id, [{ serverId: server.id, allowedTools: null }]);
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		runUtil.finish('Done anyway')
	]);
	expect(status).toBe('succeeded');
	await page.goto(`/runs/${runId}`);
	const error = timelineStep(page, 'MCP server broken is unavailable');
	await expect(error).toContainText('Error');
	await expect(error).toContainText(
		'MCP server broken is unavailable: failed to connect to broken'
	);
});

test('The servers of one workspace are invisible to another workspace and cannot be attached from it', async ({
	page,
	browser
}, testInfo) => {
	test.skip(!(await authUtil.workspacesEnabled(page)), 'workspaces are turned off');

	// Alice, the default user, has a server
	const alicesServer = await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: await unreachableMcpUrl()
	});

	// Mallory signs in with a workspace of her own, which has no servers
	const mallory = await authUtil.pageAs(browser, testInfo, {
		subject: 'mallory',
		email: 'mallory@example.com',
		name: 'Mallory'
	});
	await mallory.goto('/mcp');
	await expect(mallory.getByText('No MCP servers yet')).toBeVisible();

	// Every route for Alice's server answers as if it didn't exist
	const path = `/api/mcp-servers/${alicesServer.id}`;
	const answers = await Promise.all([
		mallory.request.get(path),
		mallory.request.put(path, { data: { name: 'stolen', transport: 'stdio', command: 'npx' } }),
		mallory.request.post(`${path}/test`),
		mallory.request.post(`${path}/oauth/login`),
		mallory.request.delete(path)
	]);
	expect(answers.map((r) => r.status())).toEqual([404, 404, 404, 404, 404]);

	// Her own job can't take Alice's server
	const mallorysJob = await runUtil.createJob(mallory.request, 'Mallory job');
	const attached = await mallory.request.put(`/api/jobs/${mallorysJob.id}/mcp-servers`, {
		data: [{ serverId: alicesServer.id, allowedTools: null }]
	});
	expect(attached.status()).toBe(404);
	expect(await attached.json()).toMatchObject({ message: 'MCP server not found' });

	// Names are unique per workspace, so she can add a notes server of her own
	await addHttpServer(mallory, { name: 'notes', url: await unreachableMcpUrl() });
	await mallory.keyboard.press('Escape');
	await expect(mallory.getByRole('dialog')).toHaveCount(0);
	const mallorysServers = await listMcpServers(mallory.request);
	expect(mallorysServers.total).toBe(1);
	expect(mallorysServers.items[0].id).not.toBe(alicesServer.id);

	// Her job can only attach her own server
	await mallory.goto(`/jobs/${mallorysJob.id}/settings`);
	await mallory.getByRole('button', { name: 'Attach server' }).click();
	await expect(mallory.getByRole('menuitem')).toHaveCount(1);
	await expect(mallory.getByRole('menuitem', { name: /notes/ })).toBeVisible();
	await mallory.keyboard.press('Escape');

	// Alice's server is untouched
	const alicesServers = await listMcpServers(page.request);
	expect(alicesServers.items.map((s) => [s.id, s.name])).toEqual([[alicesServer.id, 'notes']]);
	await mallory.context().close();
});
