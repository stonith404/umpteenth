import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { receiverHost } from './receiver.util';

// An OpenAI-compatible server on this machine that only answers the model list, as a local LLM server would
export type FakeOpenAiServer = {
	// The server's root as the backend reaches it, where /v1/models serves the model list
	url: string;
	// Replaces the model IDs the next list request returns
	setModels(ids: string[]): void;
	close(): Promise<void>;
};

// Starts the server on a free port with the given model IDs
// The root's /models answers with an HTML page, like a web UI mounted next to the API, so a base URL without /v1 reads something that isn't a model list
// It never answers with a 5xx status, since the backend's OpenAI client retries those with a backoff
export async function startFakeOpenAi(initialIds: string[]): Promise<FakeOpenAiServer> {
	let ids = [...initialIds];
	const server = http.createServer((req, res) => {
		const path = new URL(req.url ?? '/', 'http://fake').pathname;
		if (req.method === 'GET' && path === '/v1/models') {
			const data = ids.map((id) => ({ id, object: 'model', created: 0, owned_by: 'local' }));
			res.writeHead(200, { 'Content-Type': 'application/json' });
			return res.end(JSON.stringify({ object: 'list', data }));
		}
		if (req.method === 'GET' && path === '/models') {
			res.writeHead(200, { 'Content-Type': 'text/html' });
			return res.end('<!doctype html><title>Admin</title><h1>Admin</h1>');
		}
		res.writeHead(404, { 'Content-Type': 'text/plain' });
		res.end('Not found');
	});
	await new Promise<void>((resolve) => server.listen(0, '0.0.0.0', resolve));
	const port = (server.address() as AddressInfo).port;
	return {
		url: `http://${receiverHost}:${port}`,
		setModels: (next) => (ids = [...next]),
		close: () =>
			new Promise((resolve) => {
				server.closeAllConnections();
				server.close(() => resolve());
			})
	};
}
