import { expect, test as base, type APIRequestContext, type Page } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import { saveForm } from '../utils/form.util';
import {
	attachMcpServers,
	createMcpServer,
	jobMcpServers,
	mcpToolName,
	notesTools,
	startFakeMcp,
	testMcpServer,
	type FakeMcp
} from '../utils/mcp.util';
import { timelineStep } from '../utils/run-view.util';
import runUtil, { type Job } from '../utils/run.util';
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

// Runs use real sandboxes, so these specs get more time than the suite default
test.describe.configure({ timeout: 60_000 });

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// The timeline step that holds the result of the given tool call
function toolStep(page: Page, toolName: string) {
	return page.getByTestId('run-timeline').locator('[data-slot="timeline-step"]', {
		has: page.getByRole('region', { name: `Result of ${toolName}`, exact: true })
	});
}

// The step a run records when it connected to the server
function connectedStep(page: Page, server: string) {
	return timelineStep(page, `Connected to ${server}`);
}

// Registers the test's notes fake as a plain HTTP server named notes
function createNotesServer(request: APIRequestContext, fake: FakeMcp) {
	return createMcpServer(request, { name: 'notes', transport: 'http', url: fake.url });
}

// A tiny MCP server on stdin and stdout that greets with GREETING from its environment, or hi without one, and the name of the user it runs as
const ECHO_SERVER = `const send = (m) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...m }) + '\\n');
require('readline').createInterface({ input: process.stdin }).on('line', (line) => {
	const m = JSON.parse(line);
	if (m.id === undefined) return;
	const result = {
		initialize: { protocolVersion: m.params?.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: 'echo', version: '1.0.0' } },
		'tools/list': { tools: [{ name: 'whoami', description: 'Greets from the user that runs the server', inputSchema: { type: 'object', properties: {} }, annotations: { readOnlyHint: true } }] },
		'tools/call': { content: [{ type: 'text', text: (process.env.GREETING || 'hi') + ' from ' + require('os').userInfo().username }] }
	}[m.method];
	send(result ? { id: m.id, result } : { id: m.id, error: { code: -32601, message: 'Method not found' } });
});`;

test('A job run calls the tools of a plain HTTP server with arguments and shows their error results', async ({
	page,
	fake
}) => {
	// The job has no network, since the host calls HTTP servers for the sandbox
	const server = await createNotesServer(page.request, fake);
	const job = await runUtil.createJob(page.request, 'Note keeper', { network: 'none' });
	expect(job.network).toBe('none');
	await attachMcpServers(page.request, job.id, [{ serverId: server.id, allowedTools: null }]);

	// The agent adds a note and deletes one that doesn't exist
	const addNote = mcpToolName('notes', 'add_note');
	const deleteNote = mcpToolName('notes', 'delete_note');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: addNote, args: { text: 'Buy milk' } }] },
		{ toolCalls: [{ name: deleteNote, args: { id: 42 } }] },
		runUtil.finish('Added one note')
	]);
	expect(status).toBe('succeeded');

	// Both calls reached the server with their arguments
	expect(fake.toolCalls).toEqual([
		{ name: 'add_note', args: { text: 'Buy milk' } },
		{ name: 'delete_note', args: { id: 42 } }
	]);

	// The timeline shows the connection once, with the transport and the number of tools
	await page.goto(`/runs/${runId}`);
	await expect(connectedStep(page, 'notes')).toHaveCount(1);
	await expect(connectedStep(page, 'notes')).toContainText('HTTP');
	await expect(connectedStep(page, 'notes')).toContainText('3 tools');

	// The first call shows its arguments, the server it went to and what the server answered
	const addStep = toolStep(page, addNote);
	await expect(addStep.getByRole('region', { name: `Result of ${addNote}` })).toHaveText(
		'Added note 1'
	);
	await expect(addStep).toContainText('"text": "Buy milk"');
	await expect(addStep).toContainText('notes');
	await expect(addStep).not.toContainText('error');

	// The hash at the end of the name only keeps different tools apart for the model, so readers see add_note from notes
	const suffix = addNote.slice('notes__add_note'.length);
	await expect(addStep.getByText('add_note', { exact: true })).toBeVisible();
	await expect(addStep).not.toContainText(suffix);

	// The second call's error result is marked as one
	const deleteStep = toolStep(page, deleteNote);
	await expect(deleteStep.getByRole('region', { name: `Result of ${deleteNote}` })).toHaveText(
		'Note 42 not found'
	);
	await expect(deleteStep).toContainText('error');

	// The waterfall labels its bars the same way
	await page.getByRole('tab', { name: 'Waterfall' }).click();
	await expect(page.getByTestId('run-waterfall')).toContainText('add_note');
	await expect(page.getByTestId('run-waterfall')).not.toContainText(suffix);
});

test('The MCP servers card attaches servers and limits one to some of its tools', async ({
	page,
	fake
}) => {
	// The notes server was tested, so its tools are known, while the files server never was
	const notes = await createNotesServer(page.request, fake);
	await testMcpServer(page.request, notes.id);
	await createMcpServer(page.request, {
		name: 'files',
		transport: 'stdio',
		command: 'npx',
		args: ['-y', '@modelcontextprotocol/server-filesystem', '/tmp']
	});
	const job = await runUtil.createJob(page.request, 'Tool picker');

	// The job starts without servers
	await page.goto(`/jobs/${job.id}/settings`);
	const card = page.getByRole('form', { name: 'MCP servers' });
	await expect(card.getByText('No servers attached')).toBeVisible();

	// Attach both servers from the menu, which then has nothing left to offer
	await card.getByRole('button', { name: 'Attach server' }).click();
	await expect(page.getByRole('menuitem', { name: 'files stdio' })).toBeVisible();
	await page.getByRole('menuitem', { name: 'notes HTTP' }).click();
	await card.getByRole('button', { name: 'Attach server' }).click();
	await page.getByRole('menuitem', { name: 'files stdio' }).click();
	await expect(card.getByRole('button', { name: 'Attach server' })).toBeHidden();

	// A server without known tools can only get all of them until someone tests it
	const attached = card.getByRole('list', { name: 'Attached MCP servers' });
	const files = attached.getByRole('listitem').filter({ hasText: 'files' });
	await expect(files).toContainText('All tools');
	await expect(files.getByRole('link', { name: 'test the server' })).toHaveAttribute(
		'href',
		'/mcp'
	);

	// The tested server starts with every tool and marks what each tool may do
	await card.getByRole('button', { name: 'Tools of notes: All tools' }).click();
	const allTools = page.getByRole('option', { name: /^All tools/ });
	await expect(allTools).toBeChecked();
	await expect(page.getByRole('option', { name: /^list_notes/ })).toContainText('Read-only');
	await expect(page.getByRole('option', { name: /^delete_note/ })).toContainText('Destructive');

	// Leaving out the destructive tool limits the server to the other two
	await page.getByRole('option', { name: /^delete_note/ }).click();
	await expect(page.getByRole('option', { name: /^delete_note/ })).not.toBeChecked();
	await expect(allTools).toBeChecked({ indeterminate: true });
	await expect(card.getByRole('button', { name: 'Tools of notes: 2 of 3 tools' })).toBeVisible();

	// Detach the other server and save
	await page.keyboard.press('Escape');
	await card.getByRole('button', { name: 'Detach files' }).click();
	await expect(files).toBeHidden();
	await saveForm(card);
	await expect(page.getByText('Changes saved')).toBeVisible();

	// The limit survives a reload and is stored as the list of allowed tools
	await page.reload();
	await expect(card.getByRole('button', { name: 'Tools of notes: 2 of 3 tools' })).toBeVisible();
	await expect(attached.getByRole('listitem')).toHaveCount(1);
	expect(await jobMcpServers(page.request, job.id)).toEqual([
		{ serverId: notes.id, serverName: 'notes', allowedTools: ['list_notes', 'add_note'] }
	]);

	// A search treats underscores as spaces and takes its words in any order, and picking all matches brings back every tool
	await card.getByRole('button', { name: 'Tools of notes: 2 of 3 tools' }).click();
	const search = page.getByPlaceholder('Search tools');
	await search.fill('note_delete');
	await expect(page.getByRole('option', { name: /^delete_note/ })).toBeVisible();
	const allMatching = page.getByRole('option', { name: 'All matching tools 1' });
	await expect(allMatching).not.toBeChecked();
	await allMatching.click();
	await expect(card.getByRole('button', { name: 'Tools of notes: All tools' })).toBeVisible();

	// A search without matches offers nothing to pick
	await search.fill('zzz');
	await expect(page.getByText('No tools match “zzz”')).toBeVisible();
	await expect(page.getByRole('option')).toHaveCount(0);
});

test('A run limited to some tools of a server can neither see nor call the others, as the agent or through ump', async ({
	page,
	fake
}) => {
	// The job may use two of the server's three tools, as its settings would store it
	const notes = await createNotesServer(page.request, fake);
	const job = await runUtil.createJob(page.request, 'Note keeper');
	await attachMcpServers(page.request, job.id, [
		{ serverId: notes.id, allowedTools: ['list_notes', 'add_note'] }
	]);

	// The agent tries the left-out tool, a script lists and calls tools through ump, and the agent lists the notes
	const deleteNote = mcpToolName('notes', 'delete_note');
	const listNotes = mcpToolName('notes', 'list_notes');
	const script = [
		`echo "tools=$(ump mcp tools notes | jq -r '.[].name' | paste -sd, -)"`,
		`ump mcp call notes add_note '{"text":"From a script"}'`,
		`ump mcp call notes delete_note '{"id":1}' 2>&1 || echo "refused with $?"`
	].join('; ');
	const { runId, status } = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: deleteNote, args: { id: 1 } }] },
		{ toolCalls: [{ name: 'bash', args: { command: script } }] },
		{ toolCalls: [{ name: listNotes, args: {} }] },
		runUtil.finish('Listed the notes')
	]);
	expect(status).toBe('succeeded');

	// Only the allowed tools reached the server
	expect(fake.toolCalls.map((call) => call.name)).toEqual(['add_note', 'list_notes']);

	// The run only knew the two allowed tools
	await page.goto(`/runs/${runId}`);
	await expect(connectedStep(page, 'notes')).toContainText('2 tools');
	await expect(toolStep(page, deleteNote)).toContainText(
		`Unknown tool "${deleteNote}". Use one of the tools you were given.`
	);

	// ump lists the allowed tools only and refuses the other one, while the allowed call went through
	const output = page.getByTestId('run-timeline').getByRole('region', { name: 'Output of bash' });
	await expect(output).toContainText('tools=add_note,list_notes');
	await expect(output).toContainText('Added note 1');
	await expect(output).toContainText('ump mcp: MCP tool delete_note on server notes not found');
	await expect(output).toContainText('refused with 1');

	// The agent's allowed call sees the note the script added
	await expect(toolStep(page, listNotes)).toContainText('#1 From a script');
});

test('Deleting a server detaches it from its jobs, and a stale job settings page cannot attach it again', async ({
	page,
	fake
}) => {
	// The job uses one tool of a tested server
	const notes = await createNotesServer(page.request, fake);
	await testMcpServer(page.request, notes.id);
	const job = await runUtil.createJob(page.request, 'Note keeper');
	await attachMcpServers(page.request, job.id, [
		{ serverId: notes.id, allowedTools: ['list_notes'] }
	]);

	// A second tab keeps the job's settings open from before the delete
	const settings = await page.context().newPage();
	await settings.goto(`/jobs/${job.id}/settings`);
	const card = settings.getByRole('form', { name: 'MCP servers' });
	await expect(card.getByRole('button', { name: 'Tools of notes: 1 of 3 tools' })).toBeVisible();

	// Delete the server through its menu
	await page.goto('/mcp');
	await page
		.getByRole('table', { name: 'MCP servers' })
		.getByRole('button', { name: 'Actions for notes' })
		.click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const confirm = page.getByRole('alertdialog');
	await expect(confirm).toContainText('Delete notes');
	await confirm.getByRole('button', { name: 'Delete' }).click();
	await expect(page.getByText('Deleted "notes"')).toBeVisible();
	await expect(page.getByText('No MCP servers yet')).toBeVisible();

	// The job lost the server with it
	expect((await page.request.get(`/api/mcp-servers/${notes.id}`)).status()).toBe(404);
	expect(await jobMcpServers(page.request, job.id)).toEqual([]);

	// Saving the stale card fails, since the server it names is gone
	await card.getByRole('button', { name: 'Tools of notes: 1 of 3 tools' }).click();
	await settings.getByRole('option', { name: /^add_note/ }).click();
	await settings.keyboard.press('Escape');
	await expect(card.getByRole('button', { name: 'Tools of notes: 2 of 3 tools' })).toBeVisible();
	await card.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(settings.getByText('Failed to save the changes')).toBeVisible();
	await expect(settings.getByText('MCP server not found')).toBeVisible();
	await expect(card.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
	expect(await jobMcpServers(page.request, job.id)).toEqual([]);

	// Reloaded, the card has no server left to attach
	await settings.reload();
	await expect(card).toContainText('No MCP servers are configured yet. Add one first.');
	await expect(card.getByRole('button', { name: 'Attach server' })).toBeHidden();
});

test('Testing a stdio server starts it in a temporary sandbox and lists its tools', async ({
	page
}) => {
	// The server runs on the node that the default sandbox image has
	await createMcpServer(page.request, {
		name: 'echo',
		transport: 'stdio',
		command: 'node',
		args: ['-e', ECHO_SERVER]
	});

	// The test launches the server in a sandbox of its own, which takes a few seconds
	await page.goto('/mcp');
	await page
		.getByRole('table', { name: 'MCP servers' })
		.getByRole('row', { name: /echo/ })
		.getByRole('button', { name: 'Test' })
		.click();
	const testDialog = page.getByRole('dialog', { name: 'Test echo' });
	await expect(testDialog.getByText('Connected')).toBeVisible({ timeout: 30_000 });
	const tools = testDialog.getByRole('list', { name: 'Tools' }).getByRole('listitem');
	await expect(tools).toHaveCount(1);
	await expect(tools).toContainText('whoami');
});

test("A stdio server runs in the run's sandbox as the mcp user, and a root sandbox skips it only when it has environment variables", async ({
	page
}) => {
	// The echo server's greeting comes from a secret in its environment, while the plain server has no environment
	await createSecret(page.request, 'GREETING', 'hello');
	const echo = await createMcpServer(page.request, {
		name: 'echo',
		transport: 'stdio',
		command: 'node',
		args: ['-e', ECHO_SERVER],
		env: { GREETING: '{{secret:GREETING}}' }
	});
	const plain = await createMcpServer(page.request, {
		name: 'plain',
		transport: 'stdio',
		command: 'node',
		args: ['-e', ECHO_SERVER]
	});

	// A run starts the echo server in the run's sandbox as the mcp user, with the secret filled in
	const job = await runUtil.createJob(page.request, 'Greeter', { network: 'none' });
	await attachMcpServers(page.request, job.id, [{ serverId: echo.id, allowedTools: null }]);
	const echoWhoami = mcpToolName('echo', 'whoami');
	const first = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: echoWhoami }] },
		runUtil.finish('Greeted')
	]);
	expect(first.status).toBe('succeeded');
	await page.goto(`/runs/${first.runId}`);
	await expect(connectedStep(page, 'echo')).toContainText('stdio');
	await expect(toolStep(page, echoWhoami)).toContainText('hello from mcp');

	// Root could read a server's environment, so a root sandbox leaves out the echo server but still runs the plain one
	await attachMcpServers(page.request, job.id, [
		{ serverId: echo.id, allowedTools: null },
		{ serverId: plain.id, allowedTools: null }
	]);
	const patched = await page.request.patch(`/api/jobs/${job.id}`, { data: { runAsRoot: true } });
	expect(((await patched.json()) as Job).runAsRoot).toBe(true);
	const plainWhoami = mcpToolName('plain', 'whoami');
	const second = await runUtil.runScripted(page.request, job.id, [
		{ toolCalls: [{ name: echoWhoami }] },
		{ toolCalls: [{ name: plainWhoami }] },
		runUtil.finish('Greeted')
	]);
	expect(second.status).toBe('succeeded');
	await page.goto(`/runs/${second.runId}`);
	await expect(page.getByTestId('run-timeline')).toContainText(
		"MCP server echo was skipped: a stdio server with environment variables can't run in a root sandbox"
	);
	await expect(connectedStep(page, 'echo')).toHaveCount(0);
	await expect(toolStep(page, echoWhoami)).toContainText(`Unknown tool "${echoWhoami}"`);
	await expect(connectedStep(page, 'plain')).toContainText('stdio');
	await expect(toolStep(page, plainWhoami)).toContainText('hi from mcp');
});
