import { expect, type APIRequestContext } from '@playwright/test';
import { createHash } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { readBody, receiverHost } from './receiver.util';

// One tool the fake offers, answered by its handler with text or an error result
export type FakeTool = {
	name: string;
	description?: string;
	inputSchema?: Record<string, unknown>;
	annotations?: { readOnlyHint?: boolean; destructiveHint?: boolean };
	handler?: (args: Record<string, unknown>) => { text: string; isError?: boolean };
};

// A plain MCP server over Streamable HTTP without any authentication, as internal tools often run
export type FakeMcp = {
	// The MCP endpoint as the backend reaches it
	url: string;
	tools: FakeTool[];
	// Tool calls with their arguments
	toolCalls: { name: string; args: Record<string, unknown> }[];
	// The headers of every MCP request, which shows what reached the server, e.g. a secret header value
	requestHeaders: http.IncomingHttpHeaders[];
	close(): Promise<void>;
};

// The name the agent knows an MCP tool by, which mirrors ToolName in backend/internal/mcp/mcp.go
// The readable part is sanitized and truncated, and the hash suffix keeps names of different tools apart
export function mcpToolName(server: string, tool: string) {
	const readable = `${server.replace(/[^A-Za-z0-9_-]/g, '_')}__${tool.replace(/[^A-Za-z0-9_-]/g, '_')}`;

	// Go's json.Marshal escapes these characters, and the hash is taken over its output
	const identity = JSON.stringify([server, tool]).replace(
		/[<>&\u2028\u2029]/g,
		(c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`
	);
	const suffix = `__${createHash('sha256').update(identity).digest('hex').slice(0, 20)}`;
	return readable.slice(0, 64 - suffix.length) + suffix;
}

// Starts the fake on a free port with the given tools
export async function startFakeMcp(tools: FakeTool[]): Promise<FakeMcp> {
	const fake: FakeMcp = {
		url: '',
		tools,
		toolCalls: [],
		requestHeaders: [],
		close: () => new Promise((resolve) => server.close(() => resolve()))
	};

	const server = http.createServer(async (req, res) => {
		const url = new URL(req.url ?? '/', 'http://fake');
		const body = await readBody(req);
		if (url.pathname !== '/mcp') {
			res.writeHead(404, { 'Content-Type': 'text/plain' });
			return res.end('Not found');
		}
		fake.requestHeaders.push(req.headers);
		if (req.method !== 'POST') {
			res.writeHead(405, { 'Content-Type': 'text/plain' });
			return res.end('Method not allowed');
		}
		answer(res, JSON.parse(body));
	});

	// Answers every JSON-RPC request with plain JSON, and notifications with 202
	function answer(
		res: http.ServerResponse,
		msg: { id?: number | string; method: string; params?: Record<string, unknown> }
	) {
		if (msg.id === undefined) {
			res.writeHead(202);
			return res.end();
		}
		const reply = (result: unknown) => json(res, { jsonrpc: '2.0', id: msg.id, result });
		switch (msg.method) {
			case 'initialize':
				return reply({
					protocolVersion: msg.params?.protocolVersion,
					capabilities: { tools: {} },
					serverInfo: { name: 'fake-mcp', version: '1.0.0' }
				});
			case 'tools/list':
				return reply({
					tools: fake.tools.map((t) => ({
						name: t.name,
						description: t.description ?? '',
						inputSchema: t.inputSchema ?? { type: 'object', properties: {} },
						...(t.annotations ? { annotations: t.annotations } : {})
					}))
				});
			case 'tools/call': {
				const name = String(msg.params?.name);
				const args = (msg.params?.arguments ?? {}) as Record<string, unknown>;
				fake.toolCalls.push({ name, args });
				const tool = fake.tools.find((t) => t.name === name);
				if (!tool) {
					return json(res, {
						jsonrpc: '2.0',
						id: msg.id,
						error: { code: -32602, message: `Unknown tool ${name}` }
					});
				}
				const result = tool.handler?.(args) ?? { text: `${name} done` };
				return reply({
					content: [{ type: 'text', text: result.text }],
					isError: result.isError ?? false
				});
			}
			case 'ping':
				return reply({});
			default:
				return json(res, {
					jsonrpc: '2.0',
					id: msg.id,
					error: { code: -32601, message: 'Method not found' }
				});
		}
	}

	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const port = (server.address() as AddressInfo).port;
	fake.url = `http://${receiverHost}:${port}/mcp`;
	return fake;
}

function json(res: http.ServerResponse, body: unknown) {
	res.writeHead(200, { 'Content-Type': 'application/json' });
	res.end(JSON.stringify(body));
}

// A notes service with a read-only, a harmless and a destructive tool, which remembers the notes added to it
export function notesTools(): FakeTool[] {
	const notes = new Map<number, string>();
	return [
		{
			name: 'list_notes',
			description: 'List all notes',
			annotations: { readOnlyHint: true },
			handler: () => ({
				text: notes.size
					? [...notes].map(([id, text]) => `#${id} ${text}`).join('\n')
					: 'No notes yet'
			})
		},
		{
			name: 'add_note',
			description: 'Add a note',
			inputSchema: {
				type: 'object',
				properties: { text: { type: 'string' } },
				required: ['text']
			},
			annotations: { readOnlyHint: false, destructiveHint: false },
			handler: (args) => {
				const id = notes.size + 1;
				notes.set(id, String(args.text));
				return { text: `Added note ${id}` };
			}
		},
		{
			name: 'delete_note',
			description: 'Delete a note for good',
			inputSchema: {
				type: 'object',
				properties: { id: { type: 'number' } },
				required: ['id']
			},
			annotations: { readOnlyHint: false, destructiveHint: true },
			handler: (args) =>
				notes.delete(Number(args.id))
					? { text: `Deleted note ${args.id}` }
					: { text: `Note ${args.id} not found`, isError: true }
		}
	];
}

// A gate in front of a fake MCP server that turns every MCP request without the right X-Api-Key header away with a bare 401, as API-key services do
export type ApiKeyGate = {
	// The MCP endpoint as the backend reaches it
	url: string;
	// The key the gate lets through, which a test changes to follow a rotated secret
	key: string;
	// The X-Api-Key header of every MCP request, undefined for requests without one
	seenKeys: (string | undefined)[];
	close(): Promise<void>;
};

// Starts the gate on a free port and forwards what it lets through to the fake
export async function requireApiKey(fake: FakeMcp, key: string): Promise<ApiKeyGate> {
	const upstreamPort = Number(new URL(fake.url).port);
	const gate: ApiKeyGate = {
		url: '',
		key,
		seenKeys: [],
		close: () => new Promise((resolve) => server.close(() => resolve()))
	};

	const server = http.createServer((req, res) => {
		// Only the MCP endpoint is guarded, so OAuth discovery still finds nothing and the server counts as one without a login
		if (new URL(req.url ?? '/', 'http://gate').pathname === '/mcp') {
			const header = req.headers['x-api-key'];
			gate.seenKeys.push(Array.isArray(header) ? header[0] : header);
			if (header !== gate.key) {
				res.writeHead(401);
				return res.end();
			}
		}

		// Everything the gate lets through goes on to the fake unchanged, answer included
		const upstream = http.request(
			{
				host: '127.0.0.1',
				port: upstreamPort,
				path: req.url,
				method: req.method,
				headers: req.headers
			},
			(answer) => {
				res.writeHead(answer.statusCode ?? 502, answer.headers);
				answer.pipe(res);
			}
		);
		req.pipe(upstream);
	});

	// Listen on every interface, since a Dockerized backend reaches the gate through the host's address
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	gate.url = `http://${receiverHost}:${(server.address() as AddressInfo).port}/mcp`;
	return gate;
}

// An MCP URL on this machine that nothing listens on, so connecting to it is refused right away
export async function unreachableMcpUrl() {
	const server = http.createServer();
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const port = (server.address() as AddressInfo).port;
	await new Promise<void>((resolve) => server.close(() => resolve()));
	return `http://${receiverHost}:${port}/mcp`;
}

// The fields of GET /api/mcp-servers/{id} that specs check
export type McpServer = {
	id: string;
	name: string;
	description: string | null;
	transport: string;
	url: string | null;
	command: string | null;
	args: string[];
	env: Record<string, string>;
	headers: Record<string, string>;
	oauth: { clientId?: string; clientSecret?: string; scopes?: string[] };
	auth: { status: string; callbackUrl?: string };
	enabled: boolean;
	tools: { name: string; readOnly: boolean; destructive: boolean }[];
	toolsCachedAt: number | null;
};

// A server attached to a job, where null allowed tools expose every tool
export type JobMcpServer = { serverId: string; serverName?: string; allowedTools: string[] | null };

// Registers an MCP server in the workspace through the API, which neither tests it nor detects a login
export async function createMcpServer(request: APIRequestContext, body: Record<string, unknown>) {
	const response = await request.post('/api/mcp-servers', { data: body });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as McpServer;
}

// Tests the server, which caches its tools so a job can be limited to some of them, and expects it to connect
export async function testMcpServer(request: APIRequestContext, id: string) {
	const response = await request.post(`/api/mcp-servers/${id}/test`);
	expect(response.ok()).toBeTruthy();
	const result = (await response.json()) as { ok: boolean; error?: string };
	expect(result.error).toBeUndefined();
	expect(result.ok).toBe(true);
}

// Replaces the servers attached to the job
export async function attachMcpServers(
	request: APIRequestContext,
	jobId: string,
	servers: JobMcpServer[]
) {
	const response = await request.put(`/api/jobs/${jobId}/mcp-servers`, { data: servers });
	expect(response.ok()).toBeTruthy();
}

// The servers attached to the job
export async function jobMcpServers(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/mcp-servers`);
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as JobMcpServer[];
}
