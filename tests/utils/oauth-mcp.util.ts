import { createHash, randomUUID } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';

// The host name the backend uses to reach servers that specs run on this machine
// The Dockerized stack reaches the host through host.docker.internal, and a backend running directly on this machine sets RECEIVER_HOST=localhost
const receiverHost = process.env.RECEIVER_HOST ?? 'host.docker.internal';

// One authorization request the fake received
export type Authorization = {
	clientId: string;
	redirectUri: string;
	scope: string | null;
	resource: string | null;
	codeChallengeMethod: string | null;
};

// An MCP server that requires an OAuth login, with its authorization server, as Linear or Notion run them
export type FakeOAuthMcp = {
	// The MCP endpoint as the backend reaches it
	mcpUrl: string;
	// Every dynamic client registration, by client ID
	clients: Map<string, { name: string; redirectUri: string }>;
	authorizations: Authorization[];
	// How often a refresh token was redeemed
	refreshes: number;
	// Tool calls with the Authorization header they came with
	toolCalls: { name: string; authorization: string }[];
	// Answers authorization requests that ask for scopes with invalid_scope, as providers do that reject what they advertised
	rejectScopes: boolean;
	// The access token lifetime in seconds, a short one makes the backend refresh before every use
	accessTokenTtl: number;
	// Makes every refresh token invalid, as a user revoking the app's access would
	revokeRefreshTokens(): void;
	close(): Promise<void>;
};

// Starts the fake on a free port
// The consent page is served to the browser on localhost, while metadata, token and MCP URLs point where the backend reaches this machine
export async function startFakeOAuthMcp(): Promise<FakeOAuthMcp> {
	const codes = new Map<string, URLSearchParams>();
	const accessTokens = new Set<string>();
	const refreshTokens = new Set<string>();
	let issued = 0;
	let backendBase = '';
	let browserBase = '';

	const fake: FakeOAuthMcp = {
		mcpUrl: '',
		clients: new Map(),
		authorizations: [],
		refreshes: 0,
		toolCalls: [],
		rejectScopes: false,
		accessTokenTtl: 3600,
		revokeRefreshTokens: () => refreshTokens.clear(),
		close: () => new Promise((resolve) => server.close(() => resolve()))
	};

	const server = http.createServer(async (req, res) => {
		const url = new URL(req.url ?? '/', 'http://fake');
		const body = await readBody(req);
		const route = `${req.method} ${url.pathname}`;

		// Discovery: the resource names its authorization server, which publishes its endpoints
		if (route === 'GET /.well-known/oauth-protected-resource/mcp') {
			return json(res, 200, {
				resource: `${backendBase}/mcp`,
				authorization_servers: [backendBase],
				scopes_supported: ['issues:read']
			});
		}
		if (route === 'GET /.well-known/oauth-authorization-server') {
			return json(res, 200, {
				issuer: backendBase,
				authorization_endpoint: `${browserBase}/authorize`,
				token_endpoint: `${backendBase}/token`,
				registration_endpoint: `${backendBase}/register`,
				scopes_supported: ['issues:read', 'offline_access'],
				code_challenge_methods_supported: ['S256'],
				token_endpoint_auth_methods_supported: ['none'],
				authorization_response_iss_parameter_supported: true
			});
		}

		// Dynamic client registration (RFC 7591)
		if (route === 'POST /register') {
			const meta = JSON.parse(body) as { redirect_uris: string[]; client_name?: string };
			const clientId = `client-${fake.clients.size + 1}`;
			fake.clients.set(clientId, {
				name: meta.client_name ?? '',
				redirectUri: meta.redirect_uris[0]
			});
			return json(res, 201, {
				client_id: clientId,
				redirect_uris: meta.redirect_uris,
				token_endpoint_auth_method: 'none'
			});
		}

		// The consent page, or an immediate error for scopes the fake is told to reject
		if (route === 'GET /authorize') {
			const q = url.searchParams;
			fake.authorizations.push({
				clientId: q.get('client_id') ?? '',
				redirectUri: q.get('redirect_uri') ?? '',
				scope: q.get('scope'),
				resource: q.get('resource'),
				codeChallengeMethod: q.get('code_challenge_method')
			});
			if (fake.clients.get(q.get('client_id') ?? '')?.redirectUri !== q.get('redirect_uri')) {
				return text(res, 400, 'Unknown client or redirect URI');
			}
			if (fake.rejectScopes && q.get('scope')) {
				return redirect(res, q, { error: 'invalid_scope', iss: backendBase });
			}
			res.writeHead(200, { 'Content-Type': 'text/html' });
			return res.end(`<!doctype html><title>Fake Issues</title>
<h1>Fake Issues</h1>
<p>${escapeHtml(fake.clients.get(q.get('client_id') ?? '')?.name ?? '')} wants to access your issues.</p>
<p>Scopes: <code>${escapeHtml(q.get('scope') ?? 'none')}</code></p>
<form method="post" action="/authorize?${escapeHtml(url.search.slice(1))}">
<button name="decision" value="allow">Allow</button>
<button name="decision" value="deny">Deny</button>
</form>`);
		}
		if (route === 'POST /authorize') {
			const q = url.searchParams;
			if (new URLSearchParams(body).get('decision') !== 'allow') {
				return redirect(res, q, { error: 'access_denied', iss: backendBase });
			}
			const code = randomUUID();
			codes.set(code, q);
			return redirect(res, q, { code, iss: backendBase });
		}

		// The token endpoint checks PKCE, the redirect URI and the resource, and rotates refresh tokens
		if (route === 'POST /token') {
			const form = new URLSearchParams(body);
			if (form.get('grant_type') === 'authorization_code') {
				const auth = codes.get(form.get('code') ?? '');
				codes.delete(form.get('code') ?? '');
				const challenge = createHash('sha256')
					.update(form.get('code_verifier') ?? '')
					.digest('base64url');
				if (
					!auth ||
					challenge !== auth.get('code_challenge') ||
					form.get('redirect_uri') !== auth.get('redirect_uri') ||
					form.get('resource') !== auth.get('resource') ||
					form.get('client_id') !== auth.get('client_id')
				) {
					return json(res, 400, { error: 'invalid_grant' });
				}
			} else if (form.get('grant_type') === 'refresh_token') {
				if (!refreshTokens.delete(form.get('refresh_token') ?? '')) {
					return json(res, 400, { error: 'invalid_grant' });
				}
				fake.refreshes++;
			} else {
				return json(res, 400, { error: 'unsupported_grant_type' });
			}
			issued++;
			const access = `access-${issued}`;
			const refresh = `refresh-${issued}`;
			accessTokens.add(access);
			refreshTokens.add(refresh);
			return json(res, 200, {
				access_token: access,
				token_type: 'Bearer',
				refresh_token: refresh,
				expires_in: fake.accessTokenTtl
			});
		}

		// The MCP endpoint turns requests without a valid token away with a pointer to its resource metadata
		if (url.pathname === '/mcp') {
			const authorization = req.headers.authorization ?? '';
			if (!accessTokens.has(authorization.replace(/^Bearer /, ''))) {
				res.writeHead(401, {
					'WWW-Authenticate': `Bearer resource_metadata="${backendBase}/.well-known/oauth-protected-resource/mcp"`
				});
				return res.end();
			}
			if (req.method !== 'POST') return text(res, 405, 'Method not allowed');
			return mcp(res, JSON.parse(body), authorization);
		}

		text(res, 404, 'Not found');
	});

	// A minimal Streamable HTTP server that answers every request with plain JSON
	function mcp(
		res: http.ServerResponse,
		msg: { id?: number | string; method: string; params?: Record<string, unknown> },
		authorization: string
	) {
		if (msg.id === undefined) {
			res.writeHead(202);
			return res.end();
		}
		const reply = (result: unknown) => json(res, 200, { jsonrpc: '2.0', id: msg.id, result });
		switch (msg.method) {
			case 'initialize':
				return reply({
					protocolVersion: msg.params?.protocolVersion,
					capabilities: { tools: {} },
					serverInfo: { name: 'fake-issues', version: '1.0.0' }
				});
			case 'tools/list':
				return reply({
					tools: [
						{
							name: 'list_issues',
							description: 'List open issues',
							inputSchema: { type: 'object', properties: {} },
							annotations: { readOnlyHint: true, destructiveHint: false }
						}
					]
				});
			case 'tools/call':
				fake.toolCalls.push({ name: String(msg.params?.name), authorization });
				return reply({ content: [{ type: 'text', text: '#1 The login button is blue' }] });
			case 'ping':
				return reply({});
			default:
				return json(res, 200, {
					jsonrpc: '2.0',
					id: msg.id,
					error: { code: -32601, message: 'Method not found' }
				});
		}
	}

	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const port = (server.address() as AddressInfo).port;
	backendBase = `http://${receiverHost}:${port}`;
	browserBase = `http://localhost:${port}`;
	fake.mcpUrl = `${backendBase}/mcp`;
	return fake;
}

// Sends the browser back to the client's redirect URI with the outcome of the authorization request
function redirect(res: http.ServerResponse, q: URLSearchParams, params: Record<string, string>) {
	const back = new URL(q.get('redirect_uri') ?? '');
	for (const [key, value] of Object.entries({ ...params, state: q.get('state') ?? '' })) {
		back.searchParams.set(key, value);
	}
	res.writeHead(302, { Location: back.toString() });
	res.end();
}

function json(res: http.ServerResponse, status: number, body: unknown) {
	res.writeHead(status, { 'Content-Type': 'application/json' });
	res.end(JSON.stringify(body));
}

function text(res: http.ServerResponse, status: number, body: string) {
	res.writeHead(status, { 'Content-Type': 'text/plain' });
	res.end(body);
}

function readBody(req: http.IncomingMessage) {
	return new Promise<string>((resolve) => {
		let body = '';
		req.on('data', (chunk) => (body += chunk));
		req.on('end', () => resolve(body));
	});
}

function escapeHtml(value: string) {
	return value.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}
