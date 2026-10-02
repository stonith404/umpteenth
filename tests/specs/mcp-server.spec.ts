import { expect, test as base, type APIRequestContext } from '@playwright/test';
import authUtil from '../utils/auth.util';
import { cleanupBackend } from '../utils/cleanup.util';
import {
	createMcpServer,
	mcpToolName,
	notesTools,
	startFakeMcp,
	type FakeMcp
} from '../utils/mcp.util';
import runUtil from '../utils/run.util';

// Every test gets its own notes server, which is closed however the test ends
const test = base.extend<{ fake: FakeMcp }>({
	fake: async ({}, use) => {
		const fake = await startFakeMcp(notesTools());
		await use(fake);
		void fake.close();
	}
});

test.beforeEach(async ({ page }) => {
	await cleanupBackend(page.request);
	await authUtil.authenticate(page);
});

// A tool result as an MCP client reads it
type ToolResult = { content: { type: string; text: string }[]; isError?: boolean };

// An MCP client on the 2025-11-25 protocol, which Claude Code and most other clients speak, sending each message as its own POST like a stateless server expects
function mcpClient(api: APIRequestContext) {
	let id = 0;
	async function send(method: string, params: Record<string, unknown>) {
		const response = await api.post('/api/mcp', {
			headers: {
				Accept: 'application/json, text/event-stream',
				'Mcp-Protocol-Version': '2025-11-25'
			},
			data: { jsonrpc: '2.0', id: ++id, method, params }
		});
		expect(response.status()).toBe(200);
		const body = (await response.json()) as { result?: unknown; error?: { message: string } };
		expect(body.error).toBeUndefined();
		return body.result;
	}
	return {
		initialize: () =>
			send('initialize', {
				protocolVersion: '2025-11-25',
				capabilities: {},
				clientInfo: { name: 'e2e', version: '1' }
			}) as Promise<{ instructions: string }>,
		listTools: async () =>
			((await send('tools/list', {})) as { tools: { name: string }[] }).tools.map((t) => t.name),
		// Calls a tool and parses its text as JSON when it is, failing on an error result unless one is expected
		call: async (name: string, args: Record<string, unknown>) => {
			const result = (await send('tools/call', { name, arguments: args })) as ToolResult;
			return { text: result.content[0].text, isError: result.isError ?? false };
		}
	};
}

test('An agent creates a job over MCP, gives it an MCP server and runs it', async ({
	page,
	playwright,
	baseURL,
	fake
}) => {
	// The run uses a real sandbox, so this test gets more time than the suite default
	test.setTimeout(60_000);

	// The workspace has a notes server, and the agent connects with an API token
	const server = await createMcpServer(page.request, {
		name: 'notes',
		transport: 'http',
		url: fake.url
	});
	const { token } = await authUtil.createToken(page.request, 'Claude Code');
	const api = await authUtil.bearerContext(playwright, baseURL, token);
	try {
		const mcp = mcpClient(api);
		const { instructions } = await mcp.initialize();
		expect(instructions).toContain('compile_job');
		expect(await mcp.listTools()).toEqual(
			expect.arrayContaining(['create_job', 'run_job', 'get_run', 'set_job_mcp_servers'])
		);

		// The agent finds the server by name and creates a job that uses it
		const servers = await mcp.call('list_mcp_servers', { search: 'notes' });
		expect(servers.text).toContain(server.id);
		const created = await mcp.call('create_job', {
			name: 'Note keeper',
			instruction: 'Add a note that says hello.',
			network: 'none',
			selfImprove: false
		});
		expect(created.isError).toBe(false);
		const jobId = (JSON.parse(created.text) as { id: string }).id;
		const attached = await mcp.call('set_job_mcp_servers', {
			id: jobId,
			body: [{ serverId: server.id, allowedTools: null }]
		});
		expect(attached.isError).toBe(false);

		// The run calls the server's tool, and the agent follows it until it finished
		await runUtil.scriptModel(page.request, [
			{ toolCalls: [{ name: mcpToolName('notes', 'add_note'), args: { text: 'hello' } }] },
			runUtil.finish('Added the note')
		]);
		const started = await mcp.call('run_job', { id: jobId });
		const { runId } = JSON.parse(started.text) as { runId: string };
		let run: { status: string; summary: string | null; trigger: string } | undefined;
		await expect
			.poll(
				async () => {
					run = JSON.parse((await mcp.call('get_run', { id: runId })).text);
					return run!.status;
				},
				{ timeout: 45_000, intervals: [500] }
			)
			.toBe('succeeded');
		expect(run!.summary).toBe('Added the note');
		expect(run!.trigger).toBe('api');
		expect(fake.toolCalls).toEqual([{ name: 'add_note', args: { text: 'hello' } }]);

		// The job is a normal job in the app
		await page.goto(`/jobs/${jobId}`);
		await expect(page.getByRole('heading', { level: 1 })).toContainText('Note keeper');
	} finally {
		await api.dispose();
	}
});

test('A tool call that breaks a rule comes back as an error the agent can act on', async ({
	page,
	playwright,
	baseURL
}) => {
	const { token } = await authUtil.createToken(page.request, 'Agent');
	const api = await authUtil.bearerContext(playwright, baseURL, token);
	try {
		const mcp = mcpClient(api);
		await mcp.initialize();

		// A validation error names the argument the way the tool takes it
		const invalid = await mcp.call('create_job', {
			name: 'Job',
			instruction: 'x',
			network: 'moon'
		});
		expect(invalid.isError).toBe(true);
		expect(invalid.text).toContain('network:');

		const missing = await mcp.call('get_run', { id: 'does-not-exist' });
		expect(missing.isError).toBe(true);
		expect(missing.text).toContain('not_found');
	} finally {
		await api.dispose();
	}

	// Without a valid token nothing gets through
	const anonymous = await playwright.request.newContext({ baseURL });
	try {
		const response = await anonymous.post('/api/mcp', {
			headers: { Accept: 'application/json, text/event-stream', Authorization: 'Bearer ump_nope' },
			data: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} }
		});
		expect(response.status()).toBe(401);
	} finally {
		await anonymous.dispose();
	}
});

test('With a sign-in provider for MCP clients, a client without a token learns where to sign in', async ({
	playwright,
	baseURL
}) => {
	const anonymous = await playwright.request.newContext({ baseURL });
	try {
		// A client without a token gets pointed at the metadata, which only a stack with a sign-in provider for MCP clients serves
		const response = await anonymous.post('/api/mcp', {
			headers: { Accept: 'application/json, text/event-stream' },
			data: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} }
		});
		expect(response.status()).toBe(401);
		const challenge = response.headers()['www-authenticate'];
		test.skip(!challenge, 'This stack lets MCP clients use API tokens only');
		expect(challenge).toBe(
			`Bearer resource_metadata="${baseURL}/.well-known/oauth-protected-resource/api/mcp"`
		);

		// The metadata names the identity provider that issues tokens for the endpoint
		const metadata = await anonymous.get('/.well-known/oauth-protected-resource/api/mcp');
		expect(metadata.status()).toBe(200);
		expect(await metadata.json()).toMatchObject({
			resource: `${baseURL}/api/mcp`,
			authorization_servers: ['https://id.example.com']
		});
	} finally {
		await anonymous.dispose();
	}
});
